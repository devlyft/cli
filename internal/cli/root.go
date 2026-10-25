// Package cli assembles the Cobra command tree. Command groups live in
// sub-packages; this package wires them together and owns the single place
// where errors are turned into exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/auth"
	"github.com/devlyft/cli/internal/auth/credstore"
	authcmd "github.com/devlyft/cli/internal/cli/auth"
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
	apiURL    string
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
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return setup(f, &flags)
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
	pf.StringVar(&flags.apiURL, "api-url", "", "DevLyft API URL (also DEVLYFT_API_URL; default "+config.DefaultAPIURL+")")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newCompletionCmd())
	root.AddCommand(authcmd.NewCmd(f))

	return root
}

// setup validates the global flags and builds the printer and logger.
func setup(f *cmdutil.Factory, flags *globalFlags) error {
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

	setupAuth(f, flags)
	return nil
}

// setupAuth fills the API and credential dependencies tests have not
// already set.
func setupAuth(f *cmdutil.Factory, flags *globalFlags) {
	if f.Getenv == nil {
		f.Getenv = os.Getenv
	}
	if f.APIURL == "" {
		f.APIURL = config.ResolveAPIURL(flags.apiURL, f.Getenv)
	}
	if f.Credentials == nil {
		f.Credentials = sync.OnceValues(func() (credstore.Store, error) {
			dir, err := config.Dir()
			if err != nil {
				return nil, err
			}
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
			return api.New(api.Options{BaseURL: f.APIURL, Tokens: tokens, Logger: f.Log})
		}
	}
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
