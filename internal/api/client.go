// Package api is the thin facade over the generated DevLyft API clients. It
// is the only package that knows which service owns which endpoint.
//
// Commands depend on the Client interface, never on the generated packages,
// and are tested against the gomock mock in internal/api/mock.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"github.com/google/uuid"

	"github.com/devlyft/cli/internal/api/gen/deploys"
	"github.com/devlyft/cli/internal/api/gen/logs"
	"github.com/devlyft/cli/internal/api/gen/metrics"
	"github.com/devlyft/cli/internal/api/gen/org"
	"github.com/devlyft/cli/internal/version"
)

//go:generate go tool mockgen -source=client.go -destination=mock/client.go -package=mock

// Resource types, re-exported so commands need not import the generated
// packages, where they are documented.
//
//nolint:revive // aliases
type (
	Organization      = org.Organization
	Project           = org.Project
	Environment       = org.Environment
	EnvironmentSecret = org.EnvironmentSecret
	Pod               = org.Pod
	Location          = org.Location

	Build               = deploys.Build
	CreateBuildRequest  = deploys.CreateBuildRequest
	Deploy              = deploys.Deploy
	CreateDeployRequest = deploys.CreateDeployRequest
	UpdateDeployRequest = deploys.UpdateDeployRequest
	DeployEnvItem       = deploys.DeployEnvItem
	DeployEnvVar        = deploys.DeployEnvVar
	DeploySecretRef     = deploys.DeploySecretRef
	TenancyType         = deploys.TenancyType
	Route               = deploys.Route
	CreateRouteRequest  = deploys.CreateRouteRequest
	CustomDomain        = deploys.CustomDomain

	LogsResponse    = logs.LogsResponse
	LogEntry        = logs.LogEntry
	MetricsResponse = metrics.MetricsResponse
	MetricSeries    = metrics.MetricSeries

	// UUID is the type of resource IDs in requests and responses.
	UUID = uuid.UUID
)

// Client is every API call the CLI makes.
type Client interface {
	ListOrganizations(ctx context.Context) ([]Organization, error)
	GetOrganization(ctx context.Context, orgID string) (*Organization, error)
	ListProjects(ctx context.Context, orgID string) ([]Project, error)
	GetProject(ctx context.Context, projectID string) (*Project, error)
	ListEnvironments(ctx context.Context, projectID string) ([]Environment, error)
	GetEnvironment(ctx context.Context, envID string) (*Environment, error)
	ListLocations(ctx context.Context) ([]Location, error)

	ListBuilds(ctx context.Context, projectID string) ([]Build, error)
	GetBuild(ctx context.Context, buildID string) (*Build, error)
	CreateBuild(ctx context.Context, projectID string, req *CreateBuildRequest) (*Build, error)

	ListDeploys(ctx context.Context, projectID, envID string) ([]Deploy, error)
	GetDeploy(ctx context.Context, deployID string) (*Deploy, error)
	CreateDeploy(ctx context.Context, projectID, envID string, req *CreateDeployRequest) (*Deploy, error)
	// UpdateDeploy reads the deploy, applies mutate to a full update
	// request built from it and writes it back; the API replaces the whole
	// resource.
	UpdateDeploy(ctx context.Context, deployID string, mutate func(*UpdateDeployRequest) error) (*Deploy, error)
	DeleteDeploy(ctx context.Context, deployID string) error

	// ListSecrets returns up to limit secret keys (all when limit <= 0).
	// Values are never returned.
	ListSecrets(ctx context.Context, projectID, envID string, limit int) ([]EnvironmentSecret, error)
	SetSecret(ctx context.Context, projectID, envID, name, value string) error
	DeleteSecret(ctx context.Context, projectID, envID, name string) error

	// ListPods returns up to limit pods (all when limit <= 0) in scope.
	ListPods(ctx context.Context, scope PodScope, limit int) ([]Pod, error)
	GetPod(ctx context.Context, podID string) (*Pod, error)

	GetLogs(ctx context.Context, deployID string, q *LogsQuery) (*LogsResponse, error)
	// FollowLogs streams a deploy's new log lines to emit over the
	// WebSocket until ctx is cancelled, reconnecting and filling gaps from
	// GetLogs as needed. It returns ctx.Err() when cancelled.
	FollowLogs(ctx context.Context, deployID string, opts *FollowOptions, emit func(LogEntry) error) error

	ListMetricTypes(ctx context.Context) ([]string, error)
	GetDeployMetrics(ctx context.Context, deployID string, q MetricsQuery) (*MetricsResponse, error)
	GetPodMetrics(ctx context.Context, podName string, q MetricsQuery) (*MetricsResponse, error)

	ListRoutes(ctx context.Context, deployID string) ([]Route, error)
	CreateRoute(ctx context.Context, deployID string, req CreateRouteRequest) (*Route, error)
	DeleteRoute(ctx context.Context, deployID, routeID string) error

	ListDomains(ctx context.Context, orgID string) ([]CustomDomain, error)
	CreateDomain(ctx context.Context, orgID, domain string) (*CustomDomain, error)
	VerifyDomain(ctx context.Context, orgID, domain string) (*CustomDomain, error)
	DeleteDomain(ctx context.Context, orgID, domain string) error
}

// TokenSource supplies the bearer token for each request. It is asked on
// every attempt, so an implementation can refresh expired tokens.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource for a fixed token such as DEVLYFT_API_TOKEN.
type StaticToken string

// Token returns the token.
func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

// Options configure a client.
type Options struct {
	// BaseURL is the API origin, e.g. https://api.devlyft.io.
	BaseURL string
	// Tokens supplies the bearer token; nil sends no Authorization header.
	Tokens TokenSource
	// HTTPClient carries proxy and CA settings; nil uses a default client.
	HTTPClient *http.Client
	// Logger receives debug logs of each exchange, redacted; nil discards.
	Logger *slog.Logger
	// UserAgent defaults to DefaultUserAgent().
	UserAgent string
	// Retry defaults to DefaultRetryPolicy.
	Retry RetryPolicy
}

// DefaultUserAgent identifies the CLI, its version and platform.
func DefaultUserAgent() string {
	return fmt.Sprintf("devlyft/%s (%s/%s)", version.Get().Version, runtime.GOOS, runtime.GOARCH)
}

// client implements Client over the generated clients.
type client struct {
	baseURL string
	doer    *doer
	deploys *deploys.ClientWithResponses
	org     *org.ClientWithResponses
	logs    *logs.ClientWithResponses
	metrics *metrics.ClientWithResponses
}

var _ Client = (*client)(nil)

// New returns a client for the API at opts.BaseURL. Tokens are only ever
// sent over HTTPS, except to a loopback address.
func New(opts Options) (Client, error) { //nolint:gocritic // options are passed by value, like http.Server
	u, err := url.Parse(opts.BaseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid API URL %q", opts.BaseURL)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return nil, fmt.Errorf("invalid API URL %q: must use https", opts.BaseURL)
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent()
	}
	base := strings.TrimRight(opts.BaseURL, "/")
	d := newDoer(&opts)

	c := &client{baseURL: base, doer: d}
	if c.deploys, err = deploys.NewClientWithResponses(base, deploys.WithHTTPClient(d)); err != nil {
		return nil, err
	}
	if c.org, err = org.NewClientWithResponses(base, org.WithHTTPClient(d)); err != nil {
		return nil, err
	}
	if c.logs, err = logs.NewClientWithResponses(base, logs.WithHTTPClient(d)); err != nil {
		return nil, err
	}
	if c.metrics, err = metrics.NewClientWithResponses(base, metrics.WithHTTPClient(d)); err != nil {
		return nil, err
	}
	return c, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// errNoScope is returned by ListPods without a scope.
var errNoScope = errors.New("pod scope needs a project, environment or deploy ID")
