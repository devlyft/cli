package api

import (
	"context"
	"strconv"
	"time"

	"github.com/devlyft/cli/internal/api/gen/logs"
	"github.com/devlyft/cli/internal/api/gen/metrics"
	"github.com/devlyft/cli/internal/api/logstream"
)

// Logs are served by logs-api and metrics by metrics-api. Following logs
// over the WebSocket is hand-written in internal/api/logstream.

// LogsQuery narrows a log read. Empty fields use the server's defaults.
type LogsQuery struct {
	// Pod narrows to one pod name.
	Pod string
	// Since is how far back to read, as a Go duration such as "1h".
	Since string
	// Filter is a case-insensitive substring each line must contain.
	Filter string
	// Match are structured field filters such as "status=running".
	Match []string
	// Limit is the maximum number of lines.
	Limit int
}

func (c *client) GetLogs(ctx context.Context, deployID string, q *LogsQuery) (*LogsResponse, error) {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return nil, err
	}
	params := &logs.GetV1DeploysDeployIdLogsParams{
		Pod:    optString(q.Pod),
		Since:  optString(q.Since),
		Filter: optString(q.Filter),
	}
	if len(q.Match) > 0 {
		params.Match = &q.Match
	}
	if q.Limit > 0 {
		params.Limit = int32Ptr(q.Limit)
	}
	ctx, n := withAttempts(ctx)
	r, err := c.logs.GetV1DeploysDeployIdLogsWithResponse(ctx, id, params)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

// gapFillLimit is the most lines one gap-fill asks for: the API's maximum.
const gapFillLimit = 5000

// FollowOptions configure FollowLogs.
type FollowOptions struct {
	// Pod, Filter and Match narrow the stream as in LogsQuery.
	Pod    string
	Filter string
	Match  []string
	// Cursor is the cursor of the GetLogs page already shown; the stream
	// continues from it. Empty starts from the moment of connection.
	Cursor string
	// Seen are the entries already shown, so they are not repeated.
	Seen []LogEntry
	// Warn reports reconnects and gaps; nil drops them.
	Warn func(msg string)
}

func (c *client) FollowLogs(ctx context.Context, deployID string, opts *FollowOptions, emit func(LogEntry) error) error {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return err
	}
	tokens := c.doer.tokens
	if tokens == nil {
		tokens = StaticToken("")
	}
	dial := &logstream.DialOptions{
		BaseURL:  c.baseURL,
		DeployID: id.String(),
		Pod:      opts.Pod,
		Filter:   opts.Filter,
		Match:    opts.Match,
		Token:    tokens.Token,
		// The same client as every REST call, so proxy and CA settings
		// apply to the WebSocket too.
		HTTPClient: c.doer.hc,
		UserAgent:  c.doer.ua,
		Logger:     c.doer.log,
	}
	cfg := &logstream.Config{
		Dial: func(ctx context.Context) (logstream.Conn, error) { return logstream.Dial(ctx, dial) },
		History: func(ctx context.Context, since time.Duration) ([]logstream.Entry, bool, error) {
			page, err := c.GetLogs(ctx, id.String(), &LogsQuery{
				Pod:    opts.Pod,
				Filter: opts.Filter,
				Match:  opts.Match,
				Since:  roundUpSeconds(since),
				Limit:  gapFillLimit,
			})
			if err != nil {
				return nil, false, err
			}
			return page.Entries, len(page.Entries) >= gapFillLimit, nil
		},
		Seen: opts.Seen,
		Warn: opts.Warn,
	}
	if opts.Cursor != "" {
		if ts, err := time.Parse(time.RFC3339Nano, opts.Cursor); err == nil {
			cfg.Last = ts
		}
	}
	return logstream.Follow(ctx, cfg, emit)
}

// roundUpSeconds formats d as a whole number of seconds, rounded up, in
// the Go duration syntax the logs API takes for `since`.
func roundUpSeconds(d time.Duration) string {
	s := (d + time.Second - 1) / time.Second
	if s < 1 {
		s = 1
	}
	return strconv.FormatInt(int64(s), 10) + "s"
}

// MetricsQuery selects a metric series. Metric is required; empty optional
// fields use the server's defaults.
type MetricsQuery struct {
	Metric string
	// Pod narrows a deploy's metric to one pod name.
	Pod string
	// Range is how far back to read, as a Go duration such as "1h".
	Range string
	// Step is the resolution between points, as a Go duration.
	Step string
}

func (c *client) ListMetricTypes(ctx context.Context) ([]string, error) {
	ctx, n := withAttempts(ctx)
	r, err := c.metrics.GetV1MetricTypesWithResponse(ctx)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Metrics, nil
}

func (c *client) GetDeployMetrics(ctx context.Context, deployID string, q MetricsQuery) (*MetricsResponse, error) {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return nil, err
	}
	params := &metrics.GetV1DeploysDeployIdMetricsParams{
		Metric: q.Metric,
		Pod:    optString(q.Pod),
		Range:  optString(q.Range),
		Step:   optString(q.Step),
	}
	ctx, n := withAttempts(ctx)
	r, err := c.metrics.GetV1DeploysDeployIdMetricsWithResponse(ctx, id, params)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

// GetPodMetrics takes the pod's name, which is how metrics-api identifies
// pods.
func (c *client) GetPodMetrics(ctx context.Context, podName string, q MetricsQuery) (*MetricsResponse, error) {
	params := &metrics.GetV1PodsPodIdMetricsParams{
		Metric: q.Metric,
		Range:  optString(q.Range),
		Step:   optString(q.Step),
	}
	ctx, n := withAttempts(ctx)
	r, err := c.metrics.GetV1PodsPodIdMetricsWithResponse(ctx, podName, params)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
