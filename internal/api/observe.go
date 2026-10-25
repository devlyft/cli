package api

import (
	"context"

	"github.com/devlyft/cli/internal/api/gen/logs"
	"github.com/devlyft/cli/internal/api/gen/metrics"
)

// Logs are served by logs-api and metrics by metrics-api. Following logs
// over the WebSocket is hand-written in internal/api/logstream (#254).

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
