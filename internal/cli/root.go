// Package cli assembles the Cobra command tree. Command groups live in
// sub-packages; this package wires them together and owns the single place
// where errors are turned into exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/auth"
	"github.com/devlyft/cli/internal/auth/credstore"
	authcmd "github.com/devlyft/cli/internal/cli/auth"
	configcmd "github.com/devlyft/cli/internal/cli/config"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/config"
	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
	"github.com/devlyft/cli/internal/logging"
	"github.com/devlyft/cli/internal/output"
)

// globalFlags are the persistent flags every command accepts.
type globalFlags struct {
	format    string
	jq        string
	quiet     bool
	noColor   bool
	verbosity string
	scope     config.Flags
}

// NewRootCmd returns the root `devlyft` command with all subcommands attached.
// f is populated in the root's PersistentPreRunE, before any RunE.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	var flags globalFlags

	root := &cobra.Command{
		Use:           "devlyft",
		Short:         "Deploy and operate apps on DevLyft",
		Long:          "devlyft is the official command-line interface for DevLyft.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return setup(cmd, f, &flags)
		},
	}
	root.SetIn(f.IO.In)
	root.SetOut(f.IO.Out)
	root.SetErr(f.IO.ErrOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return clierr.Usagef("%v", err)
	})

	pf := root.PersistentFlags()
	pf.StringVar(&flags.format, "format", "", "Output format: table, tsv, json or yaml (default: table at a terminal, tsv otherwise)")
	pf.StringVar(&flags.jq, "jq", "", "Filter JSON output with a jq expression")
	pf.BoolVarP(&flags.quiet, "quiet", "q", false, "Print only resource IDs")
	pf.BoolVar(&flags.noColor, "no-color", false, "Disable colour (also NO_COLOR)")
	pf.StringVar(&flags.verbosity, "verbosity", "warning", "Log level for diagnostics on stderr: "+strings.Join(logging.Verbosities, ", ")+" (DEVLYFT_DEBUG=1 for debug)")
	pf.StringVar(&flags.scope.APIURL, "api-url", "", "DevLyft API URL (also "+config.EnvAPIURL+"; default "+config.DefaultAPIURL+")")
	pf.StringVar(&flags.scope.Context, "context", "", "Context to use for this command instead of the current one (also "+config.EnvContext+")")
	pf.StringVar(&flags.scope.Org, "org", "", "Organization ID, slug or name (also "+config.EnvOrg+")")
	pf.StringVar(&flags.scope.Project, "project", "", "Project ID or name (also "+config.EnvProject+")")
	pf.StringVar(&flags.scope.Env, "environment", "", "Environment ID or name (also "+config.EnvEnv+")")
	pf.StringVar(&flags.scope.Location, "location", "", "Location (also "+config.EnvLocation+")")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newCompletionCmd())
	root.AddCommand(authcmd.NewCmd(f))
	root.AddCommand(configcmd.NewConfigCmd(f))
	root.AddCommand(configcmd.NewContextCmd(f))

	return root
}

// setup validates the global flags, builds the printer and logger, loads
// the config file and resolves the scope.
func setup(cmd *cobra.Command, f *cmdutil.Factory, flags *globalFlags) error {
	format, err := output.ParseFormat(flags.format)
	if err != nil {
		return err
	}
	opts := output.Options{Format: format, JQ: flags.jq, Quiet: flags.quiet}
	f.Printer = output.NewPrinter(f.IO, opts)
	if err := opts.Validate(); err != nil {
		return err
	}

	if flags.noColor {
		f.IO.DisableColor()
	}

	verbosity := flags.verbosity
	if os.Getenv("DEVLYFT_DEBUG") == "1" {
		verbosity = "debug"
	}
	level, enabled, err := logging.ParseVerbosity(verbosity)
	if err != nil {
		return err
	}
	f.Log = logging.NewLogger(f.IO.ErrOut, level, enabled)

	if f.Getenv == nil {
		f.Getenv = os.Getenv
	}
	if annotated(cmd, cmdutil.NoConfig) {
		return nil
	}
	if f.ConfigDir == "" {
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		f.ConfigDir = dir
	}
	if f.Config == nil {
		file, err := config.Load(f.ConfigDir)
		if err != nil {
			return err
		}
		f.Config = file
	}
	if annotated(cmd, cmdutil.NoScope) {
		return nil
	}
	if f.Scope == nil {
		scope, err := config.ResolveScope(f.Config, &flags.scope, f.Getenv)
		if err != nil {
			return err
		}
		f.Scope = scope
	}
	if f.APIURL == "" {
		f.APIURL = f.Scope.APIURL.Value
	}
	setupNetwork(f, flags)
	return nil
}

// annotated reports whether cmd or one of its parents carries key.
func annotated(cmd *cobra.Command, key string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[key] == "true" {
			return true
		}
	}
	return false
}

// setupNetwork fills the HTTP, API and credential dependencies tests have
// not already set.
func setupNetwork(f *cmdutil.Factory, flags *globalFlags) {
	if f.HTTPClient == nil {
		f.HTTPClient = sync.OnceValues(func() (*http.Client, error) {
			ca := f.Scope.CABundle
			if ca.Value != "" {
				// Always shown, even with --quiet: trusting extra CAs
				// changes who can read this traffic.
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Notice: trusting extra CA certificates from %s (%s).\n", ca.Value, caSource(ca.Source))
			}
			return config.NewHTTPClient(config.HTTPOptions{CABundle: ca.Value, Getenv: f.Getenv})
		})
	}
	if f.Credentials == nil {
		f.Credentials = sync.OnceValues(func() (credstore.Store, error) {
			dir := f.ConfigDir
			timeout := credstore.TimeoutNonInteractive
			if f.IO.CanPrompt() {
				timeout = credstore.TimeoutInteractive
			}
			return credstore.New(credstore.Options{
				Dir:     dir,
				Timeout: timeout,
				Warn: func(msg string) {
					if !flags.quiet {
						_, _ = fmt.Fprintln(f.IO.ErrOut, msg)
					}
				},
			}), nil
		})
	}
	if f.NewAPIClient == nil {
		f.NewAPIClient = func(tokens api.TokenSource) (api.Client, error) {
			hc, err := f.HTTPClient()
			if err != nil {
				return nil, err
			}
			return api.New(api.Options{BaseURL: f.APIURL, Tokens: tokens, Logger: f.Log, HTTPClient: hc})
		}
	}
}

func caSource(src config.Source) string {
	if src == config.SourceEnv {
		return config.EnvCABundle
	}
	return "ca_bundle in the config file"
}

// Execute runs the CLI with args and returns the process exit code. It is
// the only place errors are printed and mapped to exit codes.
func Execute(ctx context.Context, ios *iostreams.IOStreams, args []string) int {
	f := &cmdutil.Factory{IO: ios}
	notifier := startUpdateCheck(ctx, ios, args)
	code := execute(ctx, f, NewRootCmd(f), args)
	notifier.finish(code)
	return code
}

// ExecuteWith runs the CLI in-process with a caller-supplied factory and
// any extra commands attached to the root, without the update check. Tests
// use it through internal/testutil/clitest.
func ExecuteWith(ctx context.Context, f *cmdutil.Factory, args []string, extra ...*cobra.Command) int {
	root := NewRootCmd(f)
	root.AddCommand(extra...)
	return execute(ctx, f, root, args)
}

func execute(ctx context.Context, f *cmdutil.Factory, root *cobra.Command, args []string) int {
	ios := f.IO
	root.SetArgs(args)

	// Errors returned before the root's PersistentPreRunE ran come from
	// Cobra itself: unknown commands, bad flags and argument validation.
	ranPreRun := false
	preRun := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, a []string) error {
		ranPreRun = true
		return preRun(cmd, a)
	}

	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitcode.OK
	}

	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		// Ctrl-C: no error object, even under --format json; the exit
		// code says it all.
		_, _ = fmt.Fprintln(ios.ErrOut, "Interrupted.")
		return exitcode.Interrupted
	}

	e := clierr.As(err)
	if !ranPreRun && e.Code == clierr.CodeInternal {
		e = &clierr.Error{Code: clierr.CodeInvalidArgument, Message: err.Error(), Usage: true, Err: err}
	}
	if e.Code == clierr.CodeAuthRequired && e.HTTPStatus != 0 && e.Hint == "" {
		// The API rejected the token; say which one.
		getenv := f.Getenv
		if getenv == nil {
			getenv = os.Getenv
		}
		e.Hint = auth.Hint(getenv)
	}
	if e.Usage && e.Hint == "" {
		e.Hint = fmt.Sprintf("Run '%s --help' for usage.", commandPath(root, args))
	}

	jsonErrors := wantsJSON(args)
	if f.Printer != nil {
		jsonErrors = f.Printer.Options().IsJSON()
	}
	if jsonErrors {
		_ = clierr.WriteJSON(ios.ErrOut, e)
	} else {
		_ = clierr.WriteHuman(ios.ErrOut, e, ios.ErrColorEnabled())
	}
	return e.ExitCode()
}

// commandPath returns the deepest command args resolve to, for hints.
func commandPath(root *cobra.Command, args []string) string {
	if cmd, _, err := root.Find(args); err == nil {
		return cmd.CommandPath()
	}
	return root.CommandPath()
}

// wantsJSON reports whether args ask for JSON output. It is used when an
// error happened before the flags could be validated, so that errors under
// --format json are still JSON.
func wantsJSON(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--":
			return false
		case a == "--format=json", strings.HasPrefix(a, "--jq="):
			return true
		case (a == "--format" && i+1 < len(args) && args[i+1] == "json") || a == "--jq":
			return true
		}
	}
	return false
}
