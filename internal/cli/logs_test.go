package cli_test

import (
	"context"
	"path/filepath"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/api/mock"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/testutil/clitest"
	"github.com/devlyft/cli/internal/testutil/golden"
)

const deployID = "6f1d3c2a-8a4b-4f7e-9c11-2b3d4e5f6a7b"

func logEntry(ts, pod, line string) api.LogEntry {
	e := api.LogEntry{Timestamp: ts, Line: line}
	if pod != "" {
		e.Pod = &pod
	}
	return e
}

var logPage = &api.LogsResponse{
	Entries: []api.LogEntry{
		logEntry("2026-10-25T12:00:01Z", "web-1", "listening on :8080"),
		logEntry("2026-10-25T12:00:02.5Z", "web-2", `{"level":"error","msg":"db\ttimeout"}`),
		logEntry("2026-10-25T12:00:03Z", "", "no pod"),
	},
	Cursor: "2026-10-25T12:00:03Z",
}

// followed are the lines FollowLogs delivers after the history page.
var followed = []api.LogEntry{
	logEntry("2026-10-25T12:00:04Z", "web-1", "GET / 200"),
}

func expectHistory(q *api.LogsQuery) func(m *mock.MockClient) {
	return func(m *mock.MockClient) {
		m.EXPECT().GetLogs(gomock.Any(), deployID, q).Return(logPage, nil)
	}
}

func expectFollow(q *api.LogsQuery, end error) func(m *mock.MockClient) {
	return func(m *mock.MockClient) {
		m.EXPECT().GetLogs(gomock.Any(), deployID, q).Return(logPage, nil)
		m.EXPECT().FollowLogs(gomock.Any(), deployID, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, o *api.FollowOptions, emit func(api.LogEntry) error) error {
				// The command continues from the page it printed.
				if o.Cursor != logPage.Cursor || len(o.Seen) != len(logPage.Entries) || o.Pod != q.Pod {
					return clierr.New(clierr.CodeInternal, "unexpected follow options %+v", o)
				}
				o.Warn("Log stream interrupted; reconnecting.")
				for _, e := range followed {
					if err := emit(e); err != nil {
						return err
					}
				}
				return end
			})
	}
}

//nolint:paralleltest // uses t.Setenv
func TestLogsGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("DEVLYFT_DEBUG", "")
	t.Setenv("DEVLYFT_API_TOKEN", "")

	all := &api.LogsQuery{}
	tests := []struct {
		name string
		args []string
		mock func(m *mock.MockClient)
		ctx  func(context.Context) context.Context
	}{
		{name: "logs_default", args: []string{"logs", deployID}, mock: expectHistory(all)},
		{name: "logs_timestamps", args: []string{"logs", deployID, "--timestamps"}, mock: expectHistory(all)},
		{name: "logs_json", args: []string{"logs", deployID, "--format", "json"}, mock: expectHistory(all)},
		{name: "logs_jq", args: []string{"logs", deployID, "--jq", "select(.pod == \"web-1\") | .line"}, mock: expectHistory(all)},
		{name: "logs_yaml", args: []string{"logs", deployID, "--format", "yaml"}, mock: expectHistory(all)},
		{name: "logs_tsv", args: []string{"logs", deployID, "--format", "tsv"}, mock: expectHistory(all)},
		{
			name: "logs_filters",
			args: []string{"logs", deployID, "--pod", "web-1", "--since", "15m", "--filter", "err", "--match", "level=error", "--match", "svc=~api", "--limit", "100"},
			mock: expectHistory(&api.LogsQuery{Pod: "web-1", Since: "15m", Filter: "err", Match: []string{"level=error", "svc=~api"}, Limit: 100}),
		},
		{name: "logs_follow", args: []string{"logs", deployID, "-f"}, mock: expectFollow(all, nil)},
		{name: "logs_follow_json", args: []string{"logs", deployID, "-f", "--format", "json"}, mock: expectFollow(all, nil)},
		{name: "logs_follow_pod", args: []string{"logs", deployID, "-f", "--pod", "web-1"}, mock: expectFollow(&api.LogsQuery{Pod: "web-1"}, nil)},
		{
			name: "logs_follow_interrupted",
			args: []string{"logs", deployID, "--follow"},
			mock: expectFollow(all, context.Canceled),
			ctx: func(ctx context.Context) context.Context {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx
			},
		},
		{name: "logs_follow_stream_error", args: []string{"logs", deployID, "-f"}, mock: expectFollow(all, clierr.FromHTTP(403, "log stream: forbidden", "req-3"))},
		{name: "logs_not_found", args: []string{"logs", deployID}, mock: func(m *mock.MockClient) {
			m.EXPECT().GetLogs(gomock.Any(), deployID, all).Return(nil, clierr.FromHTTP(404, "deploy not found", "req-1"))
		}},
		{name: "logs_usage_no_deploy", args: []string{"logs"}},
		{name: "logs_usage_bad_since", args: []string{"logs", deployID, "--since", "yesterday"}},
		{name: "logs_usage_negative_since", args: []string{"logs", deployID, "--since", "-5m"}},
		{name: "logs_usage_limit", args: []string{"logs", deployID, "--limit", "6000"}},
		{name: "logs_usage_quiet", args: []string{"logs", deployID, "-q"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client api.Client
			if tt.mock != nil {
				m := mock.NewMockClient(gomock.NewController(t))
				tt.mock(m)
				client = m
			} else {
				client = mock.NewMockClient(gomock.NewController(t)) // no calls expected
			}
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			res := clitest.Run(t, &clitest.Options{Args: tt.args, API: client, Context: ctx})
			golden.Assert(t, filepath.Join("testdata", tt.name+".golden"), res.Transcript())
		})
	}
}
