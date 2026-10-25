package logstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devlyft/cli/internal/clierr"
)

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func entry(sec int, line string) Entry {
	pod := "web-1"
	return Entry{Timestamp: t0.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano), Line: line, Pod: &pod}
}

// fakeConn replays entries, then ends with err.
type fakeConn struct {
	entries []Entry
	err     error
	closed  bool
}

func (c *fakeConn) Read(ctx context.Context) (Entry, error) {
	if ctx.Err() != nil {
		return Entry{}, ctx.Err()
	}
	if len(c.entries) == 0 {
		return Entry{}, c.err
	}
	e := c.entries[0]
	c.entries = c.entries[1:]
	return e, nil
}

func (c *fakeConn) Close() error { c.closed = true; return nil }

// harness drives Follow with scripted dials and history, a fake clock that
// advances by one minute per dial, and recorded sleeps.
type harness struct {
	dials   []func() (Conn, error)
	history func(since time.Duration) ([]Entry, bool, error)

	now      time.Time
	sinces   []time.Duration
	sleeps   []time.Duration
	warnings []string
	emitted  []string
}

func (h *harness) config() *Config {
	n := 0
	return &Config{
		Dial: func(context.Context) (Conn, error) {
			if n >= len(h.dials) {
				return nil, io.EOF
			}
			d := h.dials[n]
			n++
			h.now = h.now.Add(time.Minute)
			return d()
		},
		History: func(_ context.Context, since time.Duration) ([]Entry, bool, error) {
			h.sinces = append(h.sinces, since)
			if h.history == nil {
				return nil, false, nil
			}
			return h.history(since)
		},
		Warn:        func(msg string) { h.warnings = append(h.warnings, msg) },
		MaxAttempts: 3,
		Now:         func() time.Time { return h.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			return nil
		},
		Jitter: func(n int64) int64 { return n - 1 },
	}
}

func (h *harness) emit(e Entry) error {
	h.emitted = append(h.emitted, e.Line)
	return nil
}

func conn(err error, entries ...Entry) func() (Conn, error) {
	return func() (Conn, error) { return &fakeConn{entries: entries, err: err}, nil }
}

func TestFollowReauthReconnectsAndFillsGapWithoutDuplicates(t *testing.T) {
	t.Parallel()

	// A long first session (the clock moves past minSession) ends with the
	// routine 4001; the gap-fill returns one line already streamed and one
	// that was logged while disconnected.
	h := &harness{now: t0}
	h.dials = []func() (Conn, error){
		conn(fmt.Errorf("closed: %w", ErrReauth), entry(10, "a"), entry(20, "b")),
		conn(io.EOF, entry(40, "d")),
	}
	calls := 0
	h.history = func(time.Duration) ([]Entry, bool, error) {
		calls++
		if calls == 2 { // the gap-fill after the first reconnect
			return []Entry{entry(20, "b"), entry(30, "c")}, false, nil
		}
		return nil, false, nil
	}
	cfg := h.config()
	cfg.Last = t0
	cfg.Now = func() time.Time { h.now = h.now.Add(time.Minute); return h.now }

	err := Follow(t.Context(), cfg, h.emit)
	if err == nil || !strings.Contains(err.Error(), "giving up after 3 attempts") {
		t.Fatalf("Follow() = %v, want giving up", err)
	}
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(h.emitted, want) {
		t.Errorf("emitted %q, want %q", h.emitted, want)
	}
	// The second gap-fill reaches back from "now" to the newest line seen
	// (b at +20s), plus slack.
	if len(h.sinces) < 2 || h.sinces[1] < gapSlack {
		t.Errorf("gap-fill windows = %v", h.sinces)
	}
}

func TestFollowEarlyReauthIsBackedOff(t *testing.T) {
	t.Parallel()

	// The server also closes with 4001 when its tail fails; an immediate
	// close must not reconnect in a tight loop.
	h := &harness{now: t0}
	h.dials = []func() (Conn, error){
		conn(ErrReauth), conn(ErrReauth), conn(ErrReauth),
	}
	cfg := h.config()
	cfg.Now = func() time.Time { return t0 } // no time passes

	err := Follow(t.Context(), cfg, h.emit)
	if !errors.Is(err, ErrReauth) {
		t.Fatalf("Follow() = %v, want ErrReauth after giving up", err)
	}
	if len(h.sleeps) != 2 {
		t.Errorf("slept %d times, want 2 (backoff before each retry)", len(h.sleeps))
	}
}

func TestFollowBackoffGrowsAndIsCapped(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	for range 6 {
		h.dials = append(h.dials, func() (Conn, error) { return nil, errors.New("connection refused") })
	}
	cfg := h.config()
	cfg.MaxAttempts = 6
	cfg.BaseDelay = time.Second
	cfg.MaxDelay = 4 * time.Second

	err := Follow(t.Context(), cfg, h.emit)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("Follow() = %v", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	if !slices.Equal(h.sleeps, want) {
		t.Errorf("sleeps = %v, want %v", h.sleeps, want)
	}
	if len(h.warnings) != 5 {
		t.Errorf("warnings = %q, want one per retry", h.warnings)
	}
}

func TestFollowPermanentDialErrorStops(t *testing.T) {
	t.Parallel()

	for _, status := range []int{401, 403, 404} {
		h := &harness{now: t0}
		h.dials = []func() (Conn, error){
			func() (Conn, error) { return nil, clierr.FromHTTP(status, "no", "") },
		}
		err := Follow(t.Context(), h.config(), h.emit)
		if e, ok := errors.AsType[*clierr.Error](err); !ok || e.HTTPStatus != status {
			t.Errorf("status %d: Follow() = %v, want the HTTP error", status, err)
		}
		if len(h.sleeps) != 0 {
			t.Errorf("status %d: retried a permanent error", status)
		}
	}
}

func TestFollowRetriesUnavailable(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	h.dials = []func() (Conn, error){
		func() (Conn, error) { return nil, clierr.FromHTTP(503, "busy", "") },
		conn(io.EOF, entry(1, "after")),
	}
	_ = Follow(t.Context(), h.config(), h.emit)
	if !slices.Equal(h.emitted, []string{"after"}) {
		t.Errorf("emitted %q", h.emitted)
	}
}

func TestFollowSkipsSeenEntries(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF, entry(1, "old"), entry(2, "new"))}
	cfg := h.config()
	cfg.Seen = []Entry{entry(1, "old")}
	_ = Follow(t.Context(), cfg, h.emit)
	if !slices.Equal(h.emitted, []string{"new"}) {
		t.Errorf("emitted %q, want only the unseen line", h.emitted)
	}
	// Seen entries set the gap-fill start even without a cursor.
	if len(h.sinces) == 0 {
		t.Error("no gap-fill after the first connect")
	}
}

func TestFollowSameLineFromDifferentPodsIsNotDuplicate(t *testing.T) {
	t.Parallel()

	other := "web-2"
	e2 := entry(1, "ready")
	e2.Pod = &other
	h := &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF, entry(1, "ready"), e2)}
	_ = Follow(t.Context(), h.config(), h.emit)
	if len(h.emitted) != 2 {
		t.Errorf("emitted %q, want both pods' lines", h.emitted)
	}
}

func TestFollowGapFillProblemsWarn(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF, entry(100, "live"))}
	h.history = func(time.Duration) ([]Entry, bool, error) { return nil, false, errors.New("boom") }
	cfg := h.config()
	cfg.Last = t0
	_ = Follow(t.Context(), cfg, h.emit)
	if !slices.Equal(h.emitted, []string{"live"}) {
		t.Errorf("emitted %q, want the live line despite the failed gap-fill", h.emitted)
	}
	if len(h.warnings) == 0 || !strings.Contains(h.warnings[0], "boom") {
		t.Errorf("warnings = %q", h.warnings)
	}

	h = &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF)}
	h.history = func(time.Duration) ([]Entry, bool, error) { return []Entry{entry(1, "x")}, true, nil }
	cfg = h.config()
	cfg.Last = t0
	_ = Follow(t.Context(), cfg, h.emit)
	if len(h.warnings) == 0 || !strings.Contains(h.warnings[0], "may not be shown") {
		t.Errorf("truncated gap-fill did not warn: %q", h.warnings)
	}
}

func TestFollowNoGapFillWithoutStart(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF, entry(1, "x"))}
	_ = Follow(t.Context(), h.config(), h.emit)
	if len(h.sinces) != 0 {
		t.Errorf("gap-filled %v with nothing shown yet", h.sinces)
	}
}

func TestFollowEmitErrorStops(t *testing.T) {
	t.Parallel()

	h := &harness{now: t0}
	h.dials = []func() (Conn, error){conn(io.EOF, entry(1, "x"), entry(2, "y"))}
	stop := errors.New("broken pipe")
	err := Follow(t.Context(), h.config(), func(Entry) error { return stop })
	if !errors.Is(err, stop) {
		t.Errorf("Follow() = %v, want the emit error", err)
	}
}

func TestFollowCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	h := &harness{now: t0}
	c := &fakeConn{entries: []Entry{entry(1, "x")}}
	h.dials = []func() (Conn, error){func() (Conn, error) { return c, nil }}
	err := Follow(ctx, h.config(), func(Entry) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Follow() = %v, want context.Canceled", err)
	}
	if !c.closed {
		t.Error("connection not closed")
	}

	// Cancelled while backing off.
	ctx, cancel = context.WithCancel(t.Context())
	h = &harness{now: t0}
	h.dials = []func() (Conn, error){func() (Conn, error) { return nil, errors.New("down") }}
	cfg := h.config()
	cfg.Sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}
	if err := Follow(ctx, cfg, h.emit); !errors.Is(err, context.Canceled) {
		t.Errorf("Follow() while backing off = %v, want context.Canceled", err)
	}
}

func TestPruneBoundsMemory(t *testing.T) {
	t.Parallel()

	f := newFollower(&Config{}, func(Entry) error { return nil })
	for i := range maxSeen + 100 {
		if err := f.offer(&Entry{Timestamp: t0.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano), Line: "l"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.seen) > maxSeen {
		t.Errorf("remembering %d lines, want at most %d", len(f.seen), maxSeen)
	}
	// Recent lines are still remembered.
	recent := Entry{Timestamp: t0.Add(time.Duration(maxSeen+99) * time.Second).Format(time.RFC3339Nano), Line: "l"}
	if f.remember(&recent) {
		t.Error("forgot the newest line")
	}
}

func TestSleepCtx(t *testing.T) {
	t.Parallel()

	if err := sleepCtx(t.Context(), time.Millisecond); err != nil {
		t.Errorf("sleepCtx() = %v", err)
	}
}
