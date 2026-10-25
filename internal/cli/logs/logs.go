// Package logscmd implements `devlyft logs`.
package logscmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/output"
)

// maxLimit is the most lines the logs API returns in one read.
const maxLimit = 5000

type options struct {
	follow     bool
	since      string
	pod        string
	filter     string
	match      []string
	limit      int
	timestamps bool
}

// NewCmd returns the `logs` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:   "logs <deploy-id>",
		Short: "Print or follow a deploy's logs",
		Long: `Print a deploy's recent log lines, oldest first, and with --follow keep
printing new lines as they are logged until interrupted.

--follow survives the server closing the stream (it does so at least every
30 minutes to re-check access) and network drops: devlyft reconnects with
a fresh token and fills in any lines logged while it was disconnected,
without repeating lines it has already printed.

With --format json each line is printed as one JSON object per line
(newline-delimited JSON), and --jq is applied to each line:

  devlyft logs <deploy-id> --follow --format json
  devlyft logs <deploy-id> --jq 'select(.line | test("error"; "i")) | .timestamp'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, f, &o, args[0])
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&o.follow, "follow", "f", false, "Keep printing new lines until interrupted")
	fl.StringVar(&o.since, "since", "", `How far back to read, as a duration such as "15m" or "2h" (server default 1h)`)
	fl.StringVar(&o.pod, "pod", "", "Only lines from this pod (by name)")
	fl.StringVar(&o.filter, "filter", "", "Only lines containing this text (case-insensitive)")
	fl.StringArrayVar(&o.match, "match", nil, `Only JSON lines whose field matches, e.g. "level=error" or "service=~api-.*" (repeatable)`)
	fl.IntVar(&o.limit, "limit", 0, fmt.Sprintf("Most lines to read from history (server default 500, at most %d)", maxLimit))
	fl.BoolVar(&o.timestamps, "timestamps", false, "Prefix each line with its timestamp and pod")
	return cmd
}

func run(cmd *cobra.Command, f *cmdutil.Factory, o *options, deployID string) error {
	if err := o.validate(f); err != nil {
		return err
	}
	client, err := f.Client()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	page, err := client.GetLogs(ctx, deployID, &api.LogsQuery{
		Pod:    o.pod,
		Since:  o.since,
		Filter: o.filter,
		Match:  o.match,
		Limit:  o.limit,
	})
	if err != nil {
		return err
	}
	for i := range page.Entries {
		if err := printEntry(f.Printer, o, &page.Entries[i]); err != nil {
			return err
		}
	}
	if !o.follow {
		return nil
	}

	return client.FollowLogs(ctx, deployID, &api.FollowOptions{
		Pod:    o.pod,
		Filter: o.filter,
		Match:  o.match,
		Cursor: page.Cursor,
		Seen:   page.Entries,
		Warn: func(msg string) {
			// Under --format json, stderr is reserved for the error object.
			if f.Printer.Options().IsJSON() {
				f.Log.Warn(msg)
				return
			}
			_, _ = fmt.Fprintln(f.IO.ErrOut, msg)
		},
	}, func(e api.LogEntry) error {
		return printEntry(f.Printer, o, &e)
	})
}

func (o *options) validate(f *cmdutil.Factory) error {
	if f.Printer.Options().Quiet {
		return clierr.Usagef("--quiet is not supported by logs: log lines have no IDs")
	}
	if o.since != "" {
		d, err := time.ParseDuration(o.since)
		if err != nil || d <= 0 {
			return clierr.Usagef(`invalid --since %q: expected a positive duration such as "15m" or "2h"`, o.since)
		}
	}
	if o.limit < 0 || o.limit > maxLimit {
		return clierr.Usagef("invalid --limit %d: must be between 1 and %d", o.limit, maxLimit)
	}
	return nil
}

func printEntry(p *output.Printer, o *options, e *api.LogEntry) error {
	pod := ""
	if e.Pod != nil {
		pod = *e.Pod
	}
	text := e.Line
	if o.timestamps {
		text = strings.Join(nonEmpty(e.Timestamp, pod, e.Line), " ")
	}
	return p.PrintRecord(&output.Record{
		Data: e,
		Text: text,
		Row:  []string{e.Timestamp, pod, e.Line},
	})
}

func nonEmpty(ss ...string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
