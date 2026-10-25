// Package logstream follows a deploy's logs live over logs-api's WebSocket.
//
// The stream has no resume cursor: it starts at the moment of connection,
// and the server closes it with code 4001 at least every 30 minutes so the
// client re-authenticates. Follow therefore reconnects with a fresh token
// after every close or drop and fills the gap from the REST history
// endpoint, dropping lines it has already emitted. See docs/logs.md.
package logstream

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"

	"github.com/devlyft/cli/internal/api/gen/logs"
	"github.com/devlyft/cli/internal/clierr"
)

// Entry is one log line, the same shape on the WebSocket and the REST API.
type Entry = logs.LogEntry

// ErrReauth is returned by Conn.Read when the server closed the stream with
// code 4001, asking the client to re-authenticate and reconnect.
var ErrReauth = errors.New("server asked the client to re-authenticate")

// Conn is one live-tail connection.
type Conn interface {
	// Read returns the next entry. It returns ErrReauth (possibly wrapped)
	// on a 4001 close and another error on any other close or drop.
	Read(ctx context.Context) (Entry, error)
	Close() error
}

// Config wires Follow to the network. Dial and History are required.
type Config struct {
	// Dial opens a new connection with a fresh token.
	Dial func(ctx context.Context) (Conn, error)
	// History returns the entries of the last `since`, oldest first, and
	// whether the server's line limit may have cut the page short.
	History func(ctx context.Context, since time.Duration) (entries []Entry, truncated bool, err error)

	// Last is the timestamp of the newest entry already shown (or the
	// cursor from the history read); lines after it are filled from REST
	// after each connect. Zero skips the first gap-fill.
	Last time.Time
	// Seen are entries already shown, so overlaps are not repeated.
	Seen []Entry

	// Warn reports recoverable problems, such as a reconnect; nil drops them.
	Warn func(msg string)
	// MaxAttempts is how many dials or sessions in a row may fail before
	// Follow gives up. Zero means 8.
	MaxAttempts int
	// BaseDelay and MaxDelay bound the reconnect backoff. Zero means 500ms
	// and 30s.
	BaseDelay, MaxDelay time.Duration

	// Replaced in tests.
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	Jitter func(n int64) int64
}

// gapSlack widens each gap-fill window to cover clock skew between the
// client and the log store.
const gapSlack = 5 * time.Second

// minSession is how long a stream must last for its 4001 close to count as
// the routine re-authentication. The server also closes with 4001 when its
// tail fails, so an early close is backed off like a drop rather than
// retried in a tight loop.
const minSession = 10 * time.Second

// maxSeen bounds how many lines are remembered for de-duplication.
const maxSeen = 10000

// Follow streams new entries to emit until ctx is cancelled, emit returns
// an error, a dial fails permanently (401, 403, 404), or reconnecting
// fails MaxAttempts times in a row. It returns ctx.Err() when cancelled.
func Follow(ctx context.Context, cfg *Config, emit func(Entry) error) error {
	f := newFollower(cfg, emit)
	failures := 0
	for {
		started := f.now()
		conn, err := cfg.Dial(ctx)
		if err == nil {
			err = f.session(ctx, conn)
			_ = conn.Close()
		} else if !retryableDial(err) && ctx.Err() == nil {
			return err
		}

		var emitErr *emitError
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &emitErr):
			return emitErr.err
		case errors.Is(err, ErrReauth) && f.now().Sub(started) >= minSession:
			// Expected at least every 30 minutes: reconnect at once.
			failures = 0
			continue
		}

		failures++
		if failures >= f.maxAttempts {
			return fmt.Errorf("log stream: giving up after %d attempts: %w", failures, err)
		}
		if errors.Is(err, ErrReauth) {
			f.warn("Log stream closed by the server; reconnecting.")
		} else {
			f.warn(fmt.Sprintf("Log stream interrupted (%v); reconnecting.", err))
		}
		if err := f.sleep(ctx, f.backoff(failures)); err != nil {
			return err
		}
	}
}

// emitError carries an error returned by the caller's emit function.
type emitError struct{ err error }

func (e *emitError) Error() string { return e.err.Error() }

type follower struct {
	cfg  *Config
	emit func(Entry) error

	last time.Time
	seen map[uint64]time.Time

	maxAttempts         int
	baseDelay, maxDelay time.Duration
	now                 func() time.Time
	sleep               func(context.Context, time.Duration) error
	jitter              func(int64) int64
}

func newFollower(cfg *Config, emit func(Entry) error) *follower {
	f := &follower{
		cfg:         cfg,
		emit:        emit,
		last:        cfg.Last,
		seen:        make(map[uint64]time.Time),
		maxAttempts: cfg.MaxAttempts,
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		now:         cfg.Now,
		sleep:       cfg.Sleep,
		jitter:      cfg.Jitter,
	}
	if f.maxAttempts <= 0 {
		f.maxAttempts = 8
	}
	if f.baseDelay <= 0 {
		f.baseDelay = 500 * time.Millisecond
	}
	if f.maxDelay <= 0 {
		f.maxDelay = 30 * time.Second
	}
	if f.now == nil {
		f.now = time.Now
	}
	if f.sleep == nil {
		f.sleep = sleepCtx
	}
	if f.jitter == nil {
		f.jitter = rand.Int64N //nolint:gosec // jitter needs no cryptographic randomness
	}
	for i := range cfg.Seen {
		e := &cfg.Seen[i]
		f.remember(e)
		if ts := timestamp(e); ts.After(f.last) {
			f.last = ts
		}
	}
	return f
}

// session gap-fills from REST, then relays the connection until it ends.
// The gap-fill runs after connecting so nothing logged in between is lost;
// overlaps with the stream are de-duplicated.
func (f *follower) session(ctx context.Context, conn Conn) error {
	if err := f.fillGap(ctx); err != nil {
		return err
	}
	for {
		e, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if err := f.offer(&e); err != nil {
			return err
		}
	}
}

// fillGap emits what was logged between the newest entry shown and now.
func (f *follower) fillGap(ctx context.Context) error {
	if f.last.IsZero() {
		return nil
	}
	since := f.now().Sub(f.last) + gapSlack
	entries, truncated, err := f.cfg.History(ctx, since)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The live stream is still useful without the gap; say so and go on.
		f.warn(fmt.Sprintf("Could not fetch log lines missed while reconnecting: %v", err))
		return nil
	}
	if truncated {
		f.warn("Some log lines missed while reconnecting may not be shown: there were more than the server returns at once.")
	}
	for i := range entries {
		if err := f.offer(&entries[i]); err != nil {
			return err
		}
	}
	return nil
}

// offer emits e unless it was already emitted.
func (f *follower) offer(e *Entry) error {
	if !f.remember(e) {
		return nil
	}
	if err := f.emit(*e); err != nil {
		return &emitError{err: err}
	}
	if ts := timestamp(e); ts.After(f.last) {
		f.last = ts
	}
	f.prune()
	return nil
}

// remember records e and reports whether it is new. Lines are identified
// by timestamp, pod and content because the stream carries no IDs.
func (f *follower) remember(e *Entry) bool {
	ts := timestamp(e)
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d\x00", ts.UnixNano())
	if e.Pod != nil {
		_, _ = h.Write([]byte(*e.Pod))
	}
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(e.Line))
	key := h.Sum64()
	if _, ok := f.seen[key]; ok {
		return false
	}
	f.seen[key] = ts
	return true
}

// prune forgets lines too old to reappear in a gap-fill, keeping memory
// bounded on long follows.
func (f *follower) prune() {
	if len(f.seen) <= maxSeen {
		return
	}
	cutoff := f.last.Add(-2 * gapSlack)
	for k, ts := range f.seen {
		if ts.Before(cutoff) {
			delete(f.seen, k)
		}
	}
}

// backoff is exponential with full jitter.
func (f *follower) backoff(attempt int) time.Duration {
	d := float64(f.baseDelay) * math.Pow(2, float64(attempt-1))
	d = math.Min(d, float64(f.maxDelay))
	return time.Duration(f.jitter(int64(d)) + 1)
}

func (f *follower) warn(msg string) {
	if f.cfg.Warn != nil {
		f.cfg.Warn(msg)
	}
}

// timestamp parses an entry's RFC 3339 timestamp; unparsable ones are zero.
func timestamp(e *Entry) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// retryableDial reports whether a failed dial is worth retrying: network
// errors and 429/5xx are, while 401, 403 and 404 will not change.
func retryableDial(err error) bool {
	if e, ok := errors.AsType[*clierr.Error](err); ok && e.HTTPStatus != 0 {
		return e.Retryable
	}
	return true
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
