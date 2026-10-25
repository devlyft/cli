package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/logging"
)

const (
	testDeployID = "6f1c6a3e-0d6b-4f6e-9a55-3f7f1d2b9c01"
	testProject  = "0b4e1a3c-6c2f-4d0a-8c51-5f0f1e2d3c4b"
	testEnv      = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// fakeClock records waits instead of sleeping.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits = append(f.waits, d)
	f.now = f.now.Add(d)
	return ctx.Err()
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// newTestClient returns a client for srv that waits on a fake clock and
// always picks the longest jittered backoff.
func newTestClient(t *testing.T, srv *httptest.Server, log *slog.Logger) (*client, *fakeClock) {
	t.Helper()
	cl, err := New(Options{BaseURL: srv.URL, Tokens: StaticToken("s3cret-token"), HTTPClient: srv.Client(), Logger: log, UserAgent: "devlyft/test"})
	if err != nil {
		t.Fatal(err)
	}
	c := cl.(*client) //nolint:forcetypeassert // New always returns *client
	clock := &fakeClock{now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	c.doer.sleep = clock.sleep
	c.doer.now = clock.Now
	c.doer.jitter = func(n int64) int64 { return n - 1 }
	return c, clock
}

// scripted serves the given statuses in order, then 200 with body.
type scripted struct {
	mu       sync.Mutex
	statuses []int
	headers  []http.Header
	body     string
	requests []*http.Request
	bodies   []string
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	s.requests = append(s.requests, r)
	s.bodies = append(s.bodies, string(b))
	i := len(s.requests) - 1
	status := http.StatusOK
	if i < len(s.statuses) {
		status = s.statuses[i]
	}
	if i < len(s.headers) {
		for k, vs := range s.headers[i] {
			w.Header()[k] = vs
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status < 300 {
		_, _ = io.WriteString(w, s.body)
	} else {
		_, _ = io.WriteString(w, `{"error":"try later"}`)
	}
}

const deployJSON = `{"id":"` + testDeployID + `","environment_id":"` + testEnv + `","build_id":"11111111-2222-4333-8444-555555555555",` +
	`"name":"web","location_id":"eu-west","env":[{"kind":"env","key":"A","value":"1"}],"tenancy_type":"INHERIT","replicas":2,` +
	`"expose":true,"enable_hpa":false,"hpa_min_replicas":null,"hpa_max_replicas":null,"hpa_target_cpu":null,` +
	`"placement":{"state":"PLACED"},"has_registry_credential":false,"registry_credential_name":null,` +
	`"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}`

func TestRetryPolicy(t *testing.T) {
	t.Parallel()

	retryAt := time.Date(2026, 10, 25, 12, 0, 7, 0, time.UTC).Format(http.TimeFormat)
	tests := []struct {
		name      string
		statuses  []int
		headers   []http.Header
		call      func(context.Context, *client) error
		requests  int
		waits     []time.Duration
		wantExit  int
		wantInMsg string
	}{
		{
			name:     "503 then 200",
			statuses: []int{503},
			call:     getDeploy,
			requests: 2,
			waits:    []time.Duration{500 * time.Millisecond},
			wantExit: exitcode.OK,
		},
		{
			name:     "429 honours Retry-After seconds",
			statuses: []int{429},
			headers:  []http.Header{{"Retry-After": {"3"}}},
			call:     getDeploy,
			requests: 2,
			waits:    []time.Duration{3 * time.Second},
			wantExit: exitcode.OK,
		},
		{
			name:     "429 honours Retry-After date",
			statuses: []int{429},
			headers:  []http.Header{{"Retry-After": {retryAt}}},
			call:     getDeploy,
			requests: 2,
			waits:    []time.Duration{7 * time.Second},
			wantExit: exitcode.OK,
		},
		{
			name:      "Retry-After beyond MaxDelay is not waited for",
			statuses:  []int{429},
			headers:   []http.Header{{"Retry-After": {"3600"}}},
			call:      getDeploy,
			requests:  1,
			wantExit:  exitcode.Error,
			wantInMsg: "try later",
		},
		{
			name:      "gives up after MaxAttempts with the attempt count",
			statuses:  []int{500, 502, 503, 504},
			call:      getDeploy,
			requests:  4,
			waits:     []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second},
			wantExit:  exitcode.Error,
			wantInMsg: "(after 4 attempts)",
		},
		{
			name:     "PUT is retried",
			statuses: []int{200, 503},
			call: func(ctx context.Context, c *client) error {
				_, err := c.UpdateDeploy(ctx, testDeployID, func(r *UpdateDeployRequest) error { r.Replicas = 3; return nil })
				return err
			},
			requests: 3,
			waits:    []time.Duration{500 * time.Millisecond},
			wantExit: exitcode.OK,
		},
		{
			name:     "DELETE is retried",
			statuses: []int{502, 204},
			call:     func(ctx context.Context, c *client) error { return c.DeleteDeploy(ctx, testDeployID) },
			requests: 2,
			waits:    []time.Duration{500 * time.Millisecond},
			wantExit: exitcode.OK,
		},
		{
			name:     "POST is never retried",
			statuses: []int{503},
			call: func(ctx context.Context, c *client) error {
				_, err := c.CreateRoute(ctx, testDeployID, CreateRouteRequest{Hostname: "www.example.com"})
				return err
			},
			requests:  1,
			wantExit:  exitcode.Error,
			wantInMsg: "try later",
		},
		{
			name:     "4xx is not retried",
			statuses: []int{404},
			call:     getDeploy,
			requests: 1,
			wantExit: exitcode.NotFound,
		},
		{
			name:     "501 is not retried",
			statuses: []int{501},
			call:     getDeploy,
			requests: 1,
			wantExit: exitcode.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &scripted{statuses: tt.statuses, headers: tt.headers, body: deployJSON}
			srv := httptest.NewServer(s)
			defer srv.Close()
			c, clock := newTestClient(t, srv, nil)

			err := tt.call(t.Context(), c)

			if got := clierr.ExitCode(err); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d (err %v)", got, tt.wantExit, err)
			}
			if tt.wantInMsg != "" && !strings.Contains(err.Error(), tt.wantInMsg) {
				t.Errorf("error %q does not contain %q", err, tt.wantInMsg)
			}
			if len(s.requests) != tt.requests {
				t.Errorf("requests = %d, want %d", len(s.requests), tt.requests)
			}
			if !equalDurations(clock.waits, tt.waits) {
				t.Errorf("waits = %v, want %v", clock.waits, tt.waits)
			}
		})
	}
}

func getDeploy(ctx context.Context, c *client) error {
	_, err := c.GetDeploy(ctx, testDeployID)
	return err
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRetryReplaysBody(t *testing.T) {
	t.Parallel()

	s := &scripted{statuses: []int{200, 503}, body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)

	if _, err := c.UpdateDeploy(t.Context(), testDeployID, func(r *UpdateDeployRequest) error { r.Replicas = 5; return nil }); err != nil {
		t.Fatal(err)
	}
	if s.bodies[1] == "" || s.bodies[1] != s.bodies[2] {
		t.Errorf("retried PUT body differs:\n%s\n%s", s.bodies[1], s.bodies[2])
	}
}

func TestRetryTotalWaitCap(t *testing.T) {
	t.Parallel()

	s := &scripted{statuses: []int{429, 429, 429}, headers: []http.Header{{"Retry-After": {"10"}}, {"Retry-After": {"10"}}, {"Retry-After": {"10"}}}, body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, clock := newTestClient(t, srv, nil)
	c.doer.policy.MaxTotalWait = 15 * time.Second

	err := getDeploy(t.Context(), c)
	if err == nil || !strings.Contains(err.Error(), "(after 2 attempts)") {
		t.Fatalf("err = %v, want failure after 2 attempts", err)
	}
	if len(clock.waits) != 1 {
		t.Errorf("waits = %v, want one", clock.waits)
	}
}

func TestRetryStopsOnCancel(t *testing.T) {
	t.Parallel()

	s := &scripted{statuses: []int{503, 503, 503}, body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	ctx, cancel := context.WithCancel(t.Context())
	c.doer.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}

	err := getDeploy(ctx, c)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if clierr.ExitCode(err) != exitcode.Interrupted {
		t.Errorf("exit = %d, want %d", clierr.ExitCode(err), exitcode.Interrupted)
	}
	if len(s.requests) != 1 {
		t.Errorf("requests = %d, want 1", len(s.requests))
	}
}

func TestConnectionErrorsRetriedForIdempotentOnly(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	c, clock := newTestClient(t, srv, nil)
	srv.Close() // every request now fails to connect

	err := getDeploy(t.Context(), c)
	if clierr.ExitCode(err) != exitcode.Error || !strings.Contains(err.Error(), "after 4 attempts") {
		t.Errorf("GET: err = %v", err)
	}
	if e := clierr.As(err); e.Code != clierr.CodeUnavailable || !e.Retryable {
		t.Errorf("GET: code = %s retryable = %v", e.Code, e.Retryable)
	}
	if len(clock.waits) != 3 {
		t.Errorf("GET: waits = %v, want 3", clock.waits)
	}

	clock.waits = nil
	_, err = c.CreateRoute(t.Context(), testDeployID, CreateRouteRequest{Hostname: "a.example.com"})
	if err == nil || len(clock.waits) != 0 {
		t.Errorf("POST: err = %v, waits = %v; want an error and no retry", err, clock.waits)
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in     string
		want   time.Duration
		wantOK bool
	}{
		{"", 0, false},
		{"0", 0, true},
		{"5", 5 * time.Second, true},
		{"-1", 0, false},
		{"soon", 0, false},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(tt.in, now)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("parseRetryAfter(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestRequestHeaders(t *testing.T) {
	t.Parallel()

	s := &scripted{body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)

	if err := getDeploy(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	h := s.requests[0].Header
	if got := h.Get("Authorization"); got != "Bearer s3cret-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := h.Get("User-Agent"); got != "devlyft/test" {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestTokenAskedOnEveryAttempt(t *testing.T) {
	t.Parallel()

	s := &scripted{statuses: []int{503}, body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	n := 0
	c.doer.tokens = tokenFunc(func(context.Context) (string, error) {
		n++
		return "token-" + strconv.Itoa(n), nil
	})

	if err := getDeploy(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if a, b := s.requests[0].Header.Get("Authorization"), s.requests[1].Header.Get("Authorization"); a == b {
		t.Errorf("both attempts used %q; want a fresh token per attempt", a)
	}
}

type tokenFunc func(context.Context) (string, error)

func (f tokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

func TestDebugLogRedactsSecrets(t *testing.T) {
	t.Parallel()

	s := &scripted{statuses: []int{201}, body: `{"key":"DB_URL"}`}
	srv := httptest.NewServer(s)
	defer srv.Close()
	var buf bytes.Buffer
	c, _ := newTestClient(t, srv, logging.NewLogger(&buf, slog.LevelDebug, true))

	if err := c.SetSecret(t.Context(), testProject, testEnv, "DB_URL", "postgres://u:hunter2@db/x"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, secret := range []string{"s3cret-token", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("debug log leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"method=POST", "status=201", "DB_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log missing %q:\n%s", want, out)
		}
	}
}

func TestNoBodiesReadWhenNotDebugging(t *testing.T) {
	t.Parallel()

	s := &scripted{body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	var buf bytes.Buffer
	c, _ := newTestClient(t, srv, logging.NewLogger(&buf, slog.LevelWarn, true))

	if err := getDeploy(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("logged at warning level: %s", buf.String())
	}
}
