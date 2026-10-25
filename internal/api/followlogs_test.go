package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devlyft/cli/internal/testutil/apitest"
)

// countingTokens hands out tok-1, tok-2, ... so tests can check each
// reconnect asks for a fresh token.
type countingTokens struct{ n atomic.Int32 }

func (c *countingTokens) Token(context.Context) (string, error) {
	return fmt.Sprintf("tok-%d", c.n.Add(1)), nil
}

func logLine(sec int, line string) string {
	return fmt.Sprintf(`{"timestamp":"2026-10-25T12:00:%02dZ","line":%q,"pod":"web-1"}`, sec, line)
}

// End to end over a real WebSocket and the contract-checking REST fake:
// the first stream sends a line and closes with 4001 at once; the client
// reconnects with a fresh token, fills the gap from REST and carries on,
// emitting every line exactly once and in order.
func TestFollowLogsReconnectsWithFreshTokenAndFillsGap(t *testing.T) {
	t.Parallel()

	rest := apitest.NewServer(t, map[string]apitest.Response{
		"GetV1DeploysDeployIdLogs": {Body: `{"entries":[` + logLine(1, "one") + `,` + logLine(2, "two") + `],"cursor":"2026-10-25T12:00:02Z"}`},
	})

	var (
		mu       sync.Mutex
		tokens   []string
		sinces   []string
		limits   []string
		sessions int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/deploys/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sinces = append(sinces, r.URL.Query().Get("since"))
		limits = append(limits, r.URL.Query().Get("limit"))
		mu.Unlock()
		// apitest matches routes against its own address.
		r.Host = strings.TrimPrefix(rest.URL, "http://")
		rest.ServeHTTP(w, r)
	})
	mux.HandleFunc("/v1/deploys/{id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
		mu.Lock()
		tokens = append(tokens, strings.TrimSpace(parts[len(parts)-1]))
		sessions++
		n := sessions
		mu.Unlock()

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"bearer"}})
		if err != nil {
			return
		}
		if n == 1 {
			_ = c.Write(r.Context(), websocket.MessageText, []byte(logLine(1, "one")))
			_ = c.Close(4001, "reauthenticate")
			return
		}
		_ = c.Write(r.Context(), websocket.MessageText, []byte(logLine(3, "three")))
		// Hold the stream open until the client goes away.
		_, _, _ = c.Read(r.Context())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := New(Options{BaseURL: srv.URL, Tokens: &countingTokens{}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var got, warnings []string
	err = client.FollowLogs(ctx, uuidA, &FollowOptions{
		Pod:    "web-1",
		Cursor: "2026-10-25T12:00:00Z",
		Warn:   func(m string) { warnings = append(warnings, m) },
	}, func(e LogEntry) error {
		got = append(got, e.Line)
		if e.Line == "three" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FollowLogs() = %v, want context.Canceled", err)
	}
	if want := []string{"one", "two", "three"}; !slices.Equal(got, want) {
		t.Errorf("emitted %q, want %q", got, want)
	}

	mu.Lock()
	defer mu.Unlock()
	// REST gap-fills draw from the same source, so only distinctness of
	// the stream tokens is meaningful.
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Errorf("stream tokens = %q, want a fresh token per connection", tokens)
	}
	if len(sinces) != 2 {
		t.Fatalf("gap-fills = %d, want one per connection", len(sinces))
	}
	for i, s := range sinces {
		if !strings.HasSuffix(s, "s") || limits[i] != "5000" {
			t.Errorf("gap-fill %d: since=%q limit=%q", i, s, limits[i])
		}
	}
	// The early 4001 is reported like a drop rather than retried silently.
	if len(warnings) != 1 {
		t.Errorf("warnings = %q", warnings)
	}
}

func TestFollowLogsRejectsBadID(t *testing.T) {
	t.Parallel()

	client, err := New(Options{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.FollowLogs(t.Context(), "web", &FollowOptions{}, func(LogEntry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "expected a UUID") {
		t.Errorf("FollowLogs() = %v", err)
	}
}

func TestRoundUpSeconds(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"0s": "1s", "1ns": "1s", "1s": "1s", "1500ms": "2s", "2m": "120s"} {
		d, _ := parseDur(in)
		if got := roundUpSeconds(d); got != want {
			t.Errorf("roundUpSeconds(%s) = %s, want %s", in, got, want)
		}
	}
}

var parseDur = time.ParseDuration
