package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/devlyft/cli/internal/redact"
)

// RetryPolicy controls how idempotent requests are retried.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the first.
	MaxAttempts int
	// BaseDelay is the backoff before the second attempt; it doubles for
	// each further attempt, with full jitter.
	BaseDelay time.Duration
	// MaxDelay caps a single backoff, and a Retry-After the server asks for.
	// A longer Retry-After ends the retries instead.
	MaxDelay time.Duration
	// MaxTotalWait caps the time spent waiting across all retries.
	MaxTotalWait time.Duration
}

// DefaultRetryPolicy is used when Options.Retry is the zero value.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts:  4,
	BaseDelay:    500 * time.Millisecond,
	MaxDelay:     10 * time.Second,
	MaxTotalWait: 30 * time.Second,
}

// maxLoggedBody bounds request and response bodies in debug logs.
const maxLoggedBody = 4096

// doer sends requests for the generated clients: it adds the user agent
// and bearer token, retries idempotent requests and logs each exchange.
type doer struct {
	hc     *http.Client
	tokens TokenSource
	ua     string
	log    *slog.Logger
	policy RetryPolicy

	// Replaced in tests.
	sleep  func(ctx context.Context, d time.Duration) error
	now    func() time.Time
	jitter func(n int64) int64
}

func newDoer(opts *Options) *doer {
	policy := opts.Retry
	if policy == (RetryPolicy{}) {
		policy = DefaultRetryPolicy
	}
	return &doer{
		hc:     opts.HTTPClient,
		tokens: opts.Tokens,
		ua:     opts.UserAgent,
		log:    opts.Logger,
		policy: policy,
		sleep:  sleepCtx,
		now:    time.Now,
		jitter: rand.Int64N, //nolint:gosec // jitter needs no cryptographic randomness
	}
}

// idempotent methods are safe to retry. POST never is: `deploy` is safe to
// re-run because it upserts by name, not because requests are retried.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// retryableStatus reports whether a response status is worth retrying.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		(code >= http.StatusInternalServerError && code != http.StatusNotImplemented)
}

// Do implements the generated clients' HttpRequestDoer.
func (d *doer) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	attempts := attemptsFrom(ctx)
	var waited time.Duration

	for attempt := 1; ; attempt++ {
		*attempts = attempt
		r, err := d.prepare(req, attempt)
		if err != nil {
			return nil, err
		}

		start := d.now()
		resp, err := d.hc.Do(r) //nolint:gosec // the URL is the configured API origin
		d.logExchange(r, resp, err, attempt, d.now().Sub(start))

		if ctxErr := ctx.Err(); ctxErr != nil {
			closeBody(resp)
			return nil, ctxErr
		}
		retry := idempotent(req.Method) && attempt < d.policy.MaxAttempts &&
			(err != nil || retryableStatus(resp.StatusCode))
		if !retry {
			if err != nil && attempt > 1 {
				return nil, fmt.Errorf("%w (after %d attempts)", err, attempt)
			}
			return resp, err
		}

		wait, ok := d.backoff(attempt, resp)
		if !ok || waited+wait > d.policy.MaxTotalWait {
			// Out of patience: report what we have.
			if err != nil {
				return nil, fmt.Errorf("%w (after %d attempts)", err, attempt)
			}
			return resp, nil
		}
		closeBody(resp)
		if d.log.Enabled(ctx, slog.LevelDebug) {
			d.log.Debug("retrying", "method", req.Method, "url", redact.String(req.URL.String()), "attempt", attempt+1, "wait", wait)
		}
		if err := d.sleep(ctx, wait); err != nil {
			return nil, err
		}
		waited += wait
	}
}

// prepare clones req for one attempt with a fresh body and current token.
func (d *doer) prepare(req *http.Request, attempt int) (*http.Request, error) {
	r := req.Clone(req.Context())
	if attempt > 1 && req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, errors.New("request body cannot be replayed for a retry")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	if d.ua != "" {
		r.Header.Set("User-Agent", d.ua)
	}
	r.Header.Set("Accept", "application/json")
	if d.tokens != nil {
		// Asked for on every attempt so a refreshed token is picked up.
		token, err := d.tokens.Token(req.Context())
		if err != nil {
			return nil, err
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return r, nil
}

// backoff returns how long to wait before the next attempt, and false when
// the server asked for a longer wait than MaxDelay.
func (d *doer) backoff(attempt int, resp *http.Response) (time.Duration, bool) {
	if resp != nil {
		if ra, ok := parseRetryAfter(resp.Header.Get("Retry-After"), d.now()); ok {
			return ra, ra <= d.policy.MaxDelay
		}
	}
	ceiling := min(d.policy.BaseDelay<<(attempt-1), d.policy.MaxDelay)
	if ceiling <= 0 {
		return 0, true
	}
	return time.Duration(d.jitter(int64(ceiling) + 1)), true
}

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}
}

// logExchange writes one request and response to the debug log, redacted.
// Bodies are read only when debug logging is on.
func (d *doer) logExchange(req *http.Request, resp *http.Response, err error, attempt int, took time.Duration) {
	ctx := req.Context()
	if !d.log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	attrs := []any{
		"method", req.Method,
		"url", redact.String(req.URL.String()),
		"attempt", attempt,
		"duration", took.Round(time.Millisecond),
		"request_headers", redact.Header(req.Header),
	}
	if req.GetBody != nil {
		if body, berr := req.GetBody(); berr == nil {
			attrs = append(attrs, "request_body", readLogged(body))
		}
	}
	if err != nil {
		d.log.Debug("http error", append(attrs, "error", redact.String(err.Error()))...)
		return
	}
	attrs = append(attrs, "status", resp.StatusCode, "response_headers", redact.Header(resp.Header))
	if resp.Body != nil {
		// Read the body for the log and put it back for the caller.
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if rerr == nil {
			attrs = append(attrs, "response_body", truncate(b))
		}
	}
	d.log.Debug("http", attrs...)
}

func readLogged(r io.ReadCloser) string {
	defer func() { _ = r.Close() }()
	b, _ := io.ReadAll(io.LimitReader(r, maxLoggedBody+1))
	return truncate(b)
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > maxLoggedBody {
		s = s[:maxLoggedBody] + "…(truncated)"
	}
	return redact.String(s)
}

// attemptsKey carries a counter the doer updates, so errors can say how
// many attempts were made.
type attemptsKey struct{}

func withAttempts(ctx context.Context) (context.Context, *int) {
	n := new(int)
	return context.WithValue(ctx, attemptsKey{}, n), n
}

func attemptsFrom(ctx context.Context) *int {
	if n, ok := ctx.Value(attemptsKey{}).(*int); ok {
		return n
	}
	return new(int)
}
