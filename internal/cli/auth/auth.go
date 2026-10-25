// Package authcmd implements `devlyft auth`.
package authcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/auth"
	"github.com/devlyft/cli/internal/auth/credstore"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/output"
	"github.com/devlyft/cli/internal/prompt"
)

// maxTokenBytes bounds what --with-token reads from stdin.
const maxTokenBytes = 64 << 10

// NewCmd returns the `auth` command group.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in, log out and inspect credentials",
		Long: `Manage the credentials devlyft uses to call the DevLyft API.

DEVLYFT_API_TOKEN, when set, is used instead of any stored credential. Use
it in CI. Otherwise devlyft uses the credential stored by
'devlyft auth login' for the API host (--api-url / DEVLYFT_API_URL).

Stored tokens go to the system keyring (Keychain, Credential Manager or a
Secret Service provider such as gnome-keyring). Where no keyring is usable,
for example in containers, CI and WSL, they go to credentials.json in the
config directory with mode 0600, and devlyft says so once.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newLoginCmd(f), newStatusCmd(f), newLogoutCmd(f), newTokenCmd(f), newListCmd(f))
	return cmd
}

// account is the JSON and YAML shape of a credential in auth output.
type account struct {
	Host          string                   `json:"host"`
	Account       string                   `json:"account,omitempty"`
	Source        auth.Source              `json:"source"`
	Organizations []credstore.Organization `json:"organizations"`
	CreatedAt     *time.Time               `json:"created_at,omitempty"`
}

func newLoginCmd(f *cmdutil.Factory) *cobra.Command {
	var withToken, insecure bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to DevLyft",
		Long: `Log in to DevLyft and store the credential for the API host.

With --with-token, the API token is read from standard input, so it never
appears in shell history or process listings. Create a token in the
dashboard under Settings. The token is checked against the API before it
is stored.

Browser login and --no-browser are coming (devlyft/devlyft#263, #264).

In CI, set DEVLYFT_API_TOKEN instead of logging in.`,
		Example: `  devlyft auth login --with-token < token.txt
  op read op://ci/devlyft/token | devlyft auth login --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return login(cmd.Context(), f, withToken, insecure)
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read an API token from standard input")
	cmd.Flags().BoolVar(&insecure, "insecure-storage", false, "Store the token in a 0600 file instead of the system keyring")
	return cmd
}

func login(ctx context.Context, f *cmdutil.Factory, withToken, insecure bool) error {
	host := f.Host()
	if !withToken {
		e := clierr.Usagef("browser login is not available yet")
		e.Hint = "Pipe an API token instead: devlyft auth login --with-token < token.txt"
		return e
	}
	if f.Getenv(auth.EnvToken) != "" {
		e := clierr.Usagef("%s is set and takes precedence over stored credentials", auth.EnvToken)
		e.Hint = "Unset it to store a credential with `devlyft auth login`."
		return e
	}
	if f.IO.IsStdinTTY() {
		e := clierr.Usagef("--with-token reads the token from standard input, which is a terminal")
		e.Hint = "Pipe it in: devlyft auth login --with-token < token.txt"
		return e
	}
	token, err := readToken(f.IO.In)
	if err != nil {
		return err
	}

	client, err := f.NewAPIClient(api.StaticToken(token))
	if err != nil {
		return clierr.Usagef("%v", err)
	}
	orgs, err := client.ListOrganizations(ctx)
	if err != nil {
		return rejected(err, "the token was rejected by "+host, "Check the token is correct and has not expired or been deleted.")
	}

	cred := &credstore.Credential{
		Host:          host,
		Account:       f.Account(),
		Token:         token,
		Organizations: toOrganizations(orgs),
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	store, err := f.Credentials()
	if err != nil {
		return err
	}
	if err := store.Set(ctx, cred, insecure); err != nil {
		return err
	}

	a := toAccount(cred)
	a.CreatedAt = nil
	if formatRequested(f) {
		return f.Printer.Print(output.Result{Data: a, IDs: []string{host}})
	}
	where := "the system keyring"
	if cred.Store == credstore.KindFile {
		where = "credentials.json"
	}
	_, err = fmt.Fprintf(f.IO.ErrOut, "Logged in to %s (%s). Token stored in %s.\n", host, orgSummary(cred.Organizations), where)
	return err
}

// readToken reads a single token from r.
func readToken(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxTokenBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxTokenBytes {
		return "", clierr.Usagef("standard input is too long to be a token")
	}
	token := strings.TrimSpace(string(b))
	switch {
	case token == "":
		return "", clierr.Usagef("no token on standard input")
	case strings.ContainsAny(token, " \t\r\n"):
		return "", clierr.Usagef("standard input must contain only the token")
	}
	return token, nil
}

func newStatusCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which credential is in use and check it",
		Long: `Show the API host, where the credential comes from (env, keyring or
file) and the organizations it can see. The credential is checked against
the API. The token itself is never printed.

Exits 3 when not logged in or when the API rejects the credential.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			r, err := f.Resolver().Resolve(ctx, f.Host())
			if err != nil {
				return err
			}
			client, err := f.NewAPIClient(api.StaticToken(r.Token))
			if err != nil {
				return clierr.Usagef("%v", err)
			}
			orgs, err := client.ListOrganizations(ctx)
			if err != nil {
				msg := fmt.Sprintf("the stored credential for %s was rejected", r.Host)
				if r.Source == auth.SourceEnv {
					msg = fmt.Sprintf("%s was rejected by %s", auth.EnvToken, r.Host)
				}
				return rejected(err, msg, auth.Hint(f.Getenv))
			}
			a := account{Host: r.Host, Account: r.Account, Source: r.Source, Organizations: toOrganizations(orgs)}
			return f.Printer.Print(output.Result{
				Data: a,
				Table: &output.Table{
					Headers: []string{"host", "account", "source", "organizations"},
					Rows:    [][]string{{a.Host, a.Account, string(a.Source), orgNames(a.Organizations)}},
				},
				IDs: []string{a.Host},
			})
		},
	}
}

func newLogoutCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Delete the stored credential for the API host",
		Long: `Delete the stored credential for the API host from the keyring or
credentials.json.

The API token itself stays valid until it expires or is deleted in the
dashboard. DEVLYFT_API_TOKEN is not affected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			host := f.Host()
			store, err := f.Credentials()
			if err != nil {
				return err
			}
			creds, err := store.List(ctx)
			if err != nil {
				return err
			}
			if !hasCredential(creds, host, f.Account()) {
				e := auth.NotLoggedIn(host)
				if f.Getenv(auth.EnvToken) != "" {
					e.Hint = auth.EnvToken + " is set; unset it to stop using that token."
				}
				return e
			}
			ok, err := prompt.Confirm(f.IO, fmt.Sprintf("Log out of %s?", host), yes)
			if err != nil || !ok {
				return err
			}
			if err := store.Delete(ctx, host, f.Account()); err != nil && !errors.Is(err, credstore.ErrNotFound) {
				return err
			}
			if formatRequested(f) {
				return f.Printer.Print(output.Result{
					Data: map[string]any{"host": host, "account": f.Account(), "logged_out": true},
					IDs:  []string{host},
				})
			}
			msg := fmt.Sprintf("Logged out of %s.", host)
			if f.Getenv(auth.EnvToken) != "" {
				msg += " " + auth.EnvToken + " is still set and will be used."
			}
			_, err = fmt.Fprintln(f.IO.ErrOut, msg)
			return err
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, cmdutil.YesFlagUsage)
	return cmd
}

func newTokenCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the token in use (sensitive)",
		Long: `Print the token devlyft would use for the API host to standard output.

The output is a secret: anyone who sees it can act as you. Don't paste it
into issues or logs, and prefer passing it straight to another program.`,
		Example: `  curl -H "Authorization: Bearer $(devlyft auth token)" https://api.devlyft.io/v1/organizations`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := f.Resolver().Resolve(cmd.Context(), f.Host())
			if err != nil {
				return err
			}
			if formatRequested(f) {
				return f.Printer.Print(output.Result{
					Data: map[string]any{"host": r.Host, "source": r.Source, "token": r.Token},
					IDs:  []string{r.Token},
				})
			}
			_, err = fmt.Fprintln(f.IO.Out, r.Token)
			return err
		},
	}
}

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored credentials",
		Long: `List stored credentials and DEVLYFT_API_TOKEN if it is set. Read-only:
nothing is checked against the API and tokens are never printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var accounts []account
			if f.Getenv(auth.EnvToken) != "" {
				accounts = append(accounts, account{Host: f.Host(), Source: auth.SourceEnv, Organizations: []credstore.Organization{}})
			}
			store, err := f.Credentials()
			if err != nil {
				return err
			}
			creds, err := store.List(cmd.Context())
			if err != nil {
				return err
			}
			for i := range creds {
				accounts = append(accounts, toAccount(&creds[i]))
			}

			t := &output.Table{Headers: []string{"host", "account", "source", "organizations", "created"}}
			ids := make([]string, 0, len(accounts))
			for _, a := range accounts {
				created := ""
				if a.CreatedAt != nil {
					created = a.CreatedAt.Format(time.RFC3339)
				}
				t.Rows = append(t.Rows, []string{a.Host, a.Account, string(a.Source), orgNames(a.Organizations), created})
				ids = append(ids, a.Host)
			}
			if accounts == nil {
				accounts = []account{}
			}
			return f.Printer.Print(output.Result{Data: accounts, Table: t, IDs: ids})
		},
	}
}

// rejected turns an API error during a credential check into an auth error
// with a clearer message. Other errors pass through.
func rejected(err error, message, hint string) error {
	e := clierr.As(err)
	if e.Code != clierr.CodeAuthRequired && e.Code != clierr.CodePermissionDenied {
		return err
	}
	return &clierr.Error{
		Code:       clierr.CodeAuthRequired,
		Message:    message,
		Hint:       hint,
		HTTPStatus: e.HTTPStatus,
		RequestID:  e.RequestID,
		Err:        err,
	}
}

// formatRequested reports whether the user asked for machine-readable
// output; otherwise commands print a sentence to stderr.
func formatRequested(f *cmdutil.Factory) bool {
	o := f.Printer.Options()
	return o.Format != output.FormatDefault || o.JQ != "" || o.Quiet
}

func toAccount(c *credstore.Credential) account {
	src := auth.SourceFile
	if c.Store == credstore.KindKeyring {
		src = auth.SourceKeyring
	}
	orgs := c.Organizations
	if orgs == nil {
		orgs = []credstore.Organization{}
	}
	created := c.CreatedAt
	return account{Host: c.Host, Account: c.Account, Source: src, Organizations: orgs, CreatedAt: &created}
}

func toOrganizations(orgs []api.Organization) []credstore.Organization {
	out := make([]credstore.Organization, len(orgs))
	for i, o := range orgs {
		out[i] = credstore.Organization{ID: o.Id.String(), Slug: o.Slug, Name: o.Name}
	}
	return out
}

func orgNames(orgs []credstore.Organization) string {
	names := make([]string, len(orgs))
	for i, o := range orgs {
		names[i] = o.Name
		if o.Slug != "" {
			names[i] = o.Slug
		}
	}
	return strings.Join(names, ",")
}

func orgSummary(orgs []credstore.Organization) string {
	switch len(orgs) {
	case 0:
		return "no organizations"
	case 1:
		return "organization " + orgNames(orgs)
	}
	return fmt.Sprintf("%d organizations: %s", len(orgs), orgNames(orgs))
}

func hasCredential(creds []credstore.Credential, host, account string) bool {
	for _, c := range creds {
		if c.Host == host && c.Account == account {
			return true
		}
	}
	return false
}
