package cli_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/api/mock"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/output"
	"github.com/devlyft/cli/internal/prompt"
	"github.com/devlyft/cli/internal/testutil/clitest"
	"github.com/devlyft/cli/internal/testutil/golden"
)

type widget struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

var widgets = []widget{
	{ID: "0b1c", Name: "web", Labels: map[string]string{"tier": "frontend"}},
	{ID: "7f2e", Name: "worker\twith tab"},
}

const orgID = "0b4e1a3c-6c2f-4d0a-8c51-5f0f1e2d3c4b"

var (
	fixtureTime     = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fixtureProjects = []api.Project{
		{Id: uuid.MustParse("7d9f2a10-3b4c-4e5f-8a6b-1c2d3e4f5a6b"), OrganizationId: uuid.MustParse(orgID), Name: "web", CreatedAt: fixtureTime, UpdatedAt: fixtureTime},
		{Id: uuid.MustParse("9e8d7c6b-5a49-4382-a1b0-c9d8e7f6a5b4"), OrganizationId: uuid.MustParse(orgID), Name: "worker", CreatedAt: fixtureTime, UpdatedAt: fixtureTime},
	}
)

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

	// projects goes through the factory's API client, as real commands do.
	projects := &cobra.Command{
		Use:  "projects ORG",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.Client()
			if err != nil {
				return err
			}
			ps, err := c.ListProjects(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			t := &output.Table{Headers: []string{"id", "name"}}
			ids := make([]string, 0, len(ps))
			for i := range ps {
				t.Rows = append(t.Rows, []string{ps[i].Id.String(), ps[i].Name})
				ids = append(ids, ps[i].Id.String())
			}
			return f.Printer.Print(output.Result{Data: ps, Table: t, IDs: ids})
		},
	}

	return []*cobra.Command{list, describe, fail, del, projects}
}

type goldenCase struct {
	name  string
	args  []string
	tty   bool
	stdin string
	ctx   func(context.Context) context.Context
	// mock sets expectations on the API client; nil runs without one.
	mock func(*mock.MockClient)
}

// client returns a gomock API client with the case's expectations, or nil.
func (c *goldenCase) client(t *testing.T) api.Client {
	t.Helper()
	if c.mock == nil {
		return nil
	}
	m := mock.NewMockClient(gomock.NewController(t))
	c.mock(m)
	return m
}

//nolint:paralleltest // uses t.Setenv
func TestGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("DEVLYFT_DEBUG", "")
	t.Setenv("DEVLYFT_API_TOKEN", "")

	cancelled := func(ctx context.Context) context.Context {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}

	tests := []goldenCase{
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

		// API client injected through the factory (gomock).
		{name: "api_list", args: []string{"projects", orgID}, mock: func(m *mock.MockClient) {
			m.EXPECT().ListProjects(gomock.Any(), orgID).Return(fixtureProjects, nil)
		}},
		{name: "api_list_json", args: []string{"projects", orgID, "--format", "json"}, mock: func(m *mock.MockClient) {
			m.EXPECT().ListProjects(gomock.Any(), orgID).Return(fixtureProjects, nil)
		}},
		{name: "api_not_found", args: []string{"projects", orgID}, mock: func(m *mock.MockClient) {
			m.EXPECT().ListProjects(gomock.Any(), orgID).Return(nil, clierr.FromHTTP(404, "organization not found", "req-9"))
		}},
		{name: "api_not_configured", args: []string{"projects", orgID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			res := clitest.Run(t, &clitest.Options{
				Args:     tt.args,
				TTY:      tt.tty,
				Stdin:    tt.stdin,
				API:      tt.client(t),
				Commands: fixtureCommands,
				Context:  ctx,
			})
			golden.Assert(t, filepath.Join("testdata", tt.name+".golden"), res.Transcript())
		})
	}
}
