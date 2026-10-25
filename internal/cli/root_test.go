package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/iostreams"
	"github.com/devlyft/cli/internal/output"
	"github.com/devlyft/cli/internal/prompt"
)

var update = flag.Bool("update", false, "rewrite golden files")

type widget struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

var widgets = []widget{
	{ID: "0b1c", Name: "web", Labels: map[string]string{"tier": "frontend"}},
	{ID: "7f2e", Name: "worker\twith tab"},
}

// fixtureCommands are stand-ins for real commands, exercising the printer,
// the error mapping and confirmation.
func fixtureCommands(f *cmdutil.Factory) []*cobra.Command {
	list := &cobra.Command{
		Use: "widgets",
		RunE: func(*cobra.Command, []string) error {
			t := &output.Table{Headers: []string{"id", "name"}}
			ids := make([]string, 0, len(widgets))
			for _, w := range widgets {
				t.Rows = append(t.Rows, []string{w.ID, w.Name})
				ids = append(ids, w.ID)
			}
			return f.Printer.Print(output.Result{Data: widgets, Table: t, IDs: ids})
		},
	}
	describe := &cobra.Command{
		Use: "widget",
		RunE: func(*cobra.Command, []string) error {
			return f.Printer.Print(output.Result{Data: widgets[0], IDs: []string{widgets[0].ID}})
		},
	}
	var status int
	var code, requestID string
	fail := &cobra.Command{
		Use:  "fail",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			switch {
			case status != 0:
				return clierr.FromHTTP(status, "server said no", requestID)
			case code == "plain":
				return errors.New("dial tcp: connection refused")
			case code == "deadline":
				return fmt.Errorf("waiting: %w", context.DeadlineExceeded)
			case code == "canceled":
				return context.Canceled
			}
			e := clierr.New(clierr.Code(code), "failed with %s", code)
			e.Hint = "try again"
			e.Details = map[string]any{"deploy": "web"}
			return e
		},
	}
	fail.Flags().IntVar(&status, "status", 0, "")
	fail.Flags().StringVar(&code, "code", "", "")
	fail.Flags().StringVar(&requestID, "request-id", "", "")

	var yes bool
	del := &cobra.Command{
		Use: "delete",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := prompt.Confirm(f.IO, "Delete widget web?", yes)
			if err != nil {
				return err
			}
			if ok {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "deleted")
			}
			return err
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, cmdutil.YesFlagUsage)

	return []*cobra.Command{list, describe, fail, del}
}

type golden struct {
	name  string
	args  []string
	tty   bool
	stdin string
	ctx   func(context.Context) context.Context
}

//nolint:paralleltest // uses t.Setenv
func TestGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("DEVLYFT_DEBUG", "")

	cancelled := func(ctx context.Context) context.Context {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}

	tests := []golden{
		// Formats.
		{name: "list_default_pipe", args: []string{"widgets"}},
		{name: "list_default_tty", args: []string{"widgets"}, tty: true},
		{name: "list_default_tty_no_color", args: []string{"widgets", "--no-color"}, tty: true},
		{name: "list_table", args: []string{"widgets", "--format", "table"}},
		{name: "list_tsv", args: []string{"widgets", "--format=tsv"}, tty: true},
		{name: "list_json", args: []string{"widgets", "--format", "json"}},
		{name: "list_yaml", args: []string{"widgets", "--format", "yaml"}},
		{name: "list_jq_strings", args: []string{"widgets", "--jq", ".[].name"}},
		{name: "list_jq_objects", args: []string{"widgets", "--format", "json", "--jq", ".[] | {id}"}},
		{name: "list_quiet", args: []string{"widgets", "-q"}},
		{name: "describe_default", args: []string{"widget"}, tty: true},
		{name: "describe_json", args: []string{"widget", "--format", "json"}},

		// Usage errors: exit 2.
		{name: "usage_unknown_command", args: []string{"nope"}},
		{name: "usage_unknown_flag", args: []string{"widgets", "--bogus"}},
		{name: "usage_unknown_flag_json", args: []string{"widgets", "--bogus", "--format", "json"}},
		{name: "usage_extra_args", args: []string{"fail", "extra"}},
		{name: "usage_bad_format", args: []string{"widgets", "--format", "xml"}},
		{name: "usage_jq_with_yaml", args: []string{"widgets", "--format", "yaml", "--jq", "."}},
		{name: "usage_bad_jq", args: []string{"widgets", "--jq", ".["}},
		{name: "usage_quiet_with_json", args: []string{"widgets", "-q", "--format", "json"}},
		{name: "usage_bad_verbosity", args: []string{"widgets", "--verbosity", "loud"}},

		// Each error code, human and JSON.
		{name: "err_auth_required", args: []string{"fail", "--code", "auth_required"}},
		{name: "err_auth_required_json", args: []string{"fail", "--code", "auth_required", "--format", "json"}},
		{name: "err_permission_denied", args: []string{"fail", "--code", "permission_denied"}},
		{name: "err_not_found", args: []string{"fail", "--code", "not_found"}},
		{name: "err_conflict", args: []string{"fail", "--code", "conflict"}},
		{name: "err_invalid_argument", args: []string{"fail", "--code", "invalid_argument"}},
		{name: "err_timeout", args: []string{"fail", "--code", "timeout"}},
		{name: "err_placement_pending", args: []string{"fail", "--code", "placement_pending"}},
		{name: "err_rollout_failed", args: []string{"fail", "--code", "rollout_failed"}},
		{name: "err_unavailable", args: []string{"fail", "--code", "unavailable"}},
		{name: "err_internal", args: []string{"fail", "--code", "internal"}},
		{name: "err_plain", args: []string{"fail", "--code", "plain"}},
		{name: "err_deadline", args: []string{"fail", "--code", "deadline"}},

		// API errors.
		{name: "http_401", args: []string{"fail", "--status", "401"}},
		{name: "http_403_json", args: []string{"fail", "--status", "403", "--request-id", "req-123", "--jq", "."}},
		{name: "http_404", args: []string{"fail", "--status", "404", "--request-id", "req-123"}},
		{name: "http_409", args: []string{"fail", "--status", "409"}},
		{name: "http_412", args: []string{"fail", "--status", "412"}},
		{name: "http_422", args: []string{"fail", "--status", "422"}},
		{name: "http_429_json", args: []string{"fail", "--status", "429", "--format", "json"}},
		{name: "http_503", args: []string{"fail", "--status", "503"}},

		// Interrupt: exit 130.
		{name: "interrupted", args: []string{"fail", "--code", "canceled"}, ctx: cancelled},
		{name: "interrupted_json", args: []string{"fail", "--code", "canceled", "--format", "json"}, ctx: cancelled},

		// Confirmation.
		{name: "confirm_yes_flag", args: []string{"delete", "--yes"}},
		{name: "confirm_no_tty", args: []string{"delete"}},
		{name: "confirm_tty_yes", args: []string{"delete"}, tty: true, stdin: "y\n"},
		{name: "confirm_tty_no", args: []string{"delete"}, tty: true, stdin: "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, in, stdout, stderr := iostreams.Test()
			ios.SetTTY(tt.tty)
			in.WriteString(tt.stdin)

			f := &cmdutil.Factory{IO: ios}
			root := NewRootCmd(f)
			root.AddCommand(fixtureCommands(f)...)

			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			code := execute(ctx, f, root, tt.args)

			got := fmt.Sprintf("$ devlyft %s\n--- exit %d\n--- stdout\n%s--- stderr\n%s",
				strings.Join(tt.args, " "), code, stdout.String(), stderr.String())
			assertGolden(t, filepath.Join("testdata", tt.name+".golden"), got)
		})
	}
}

func assertGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("reading golden file (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s (run with -update to accept)\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestExecuteVersion(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := iostreams.Test()
	code := Execute(t.Context(), ios, []string{"version"})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "devlyft ") {
		t.Errorf("stdout = %q, want prefix %q", stdout.String(), "devlyft ")
	}
}

func TestWantsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"x", "--format", "json"}, true},
		{[]string{"x", "--format=json"}, true},
		{[]string{"x", "--jq", "."}, true},
		{[]string{"x", "--jq=."}, true},
		{[]string{"x", "--format", "yaml"}, false},
		{[]string{"x", "--", "--format", "json"}, false},
		{[]string{"x"}, false},
	}
	for _, tt := range tests {
		if got := wantsJSON(tt.args); got != tt.want {
			t.Errorf("wantsJSON(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
