package logstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

const deployID = "6f1d3c2a-8a4b-4f7e-9c11-2b3d4e5f6a7b"

// wsServer is a minimal logs-api stream: it checks the bearer subprotocol,
// sends frames and closes with the given code.
func wsServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handle))
	t.Cleanup(srv.Close)
	return srv
}

func acceptAndSend(t *testing.T, w http.ResponseWriter, r *http.Request, frames []string, code websocket.StatusCode) {
	t.Helper()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"bearer"}})
	if err != nil {
		t.Errorf("accept: %v", err)
		return
	}
	for _, f := range frames {
		if err := c.Write(r.Context(), websocket.MessageText, []byte(f)); err != nil {
			t.Errorf("write: %v", err)
			return
		}
	}
	_ = c.Close(code, "reauthenticate")
}

func TestDialSendsTokenInSubprotocolAndReadsFrames(t *testing.T) {
	t.Parallel()

	srv := wsServer(t, func(w http.ResponseWriter, r *http.Request) {
		// logs-api splits the header on commas and trims each value.
		got := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
		if len(got) != 2 || strings.TrimSpace(got[0]) != "bearer" || strings.TrimSpace(got[1]) != "tok-1" {
			t.Errorf("Sec-WebSocket-Protocol = %q, want bearer and the token", got)
		}
		if r.URL.Path != "/v1/deploys/"+deployID+"/logs/stream" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if q := r.URL.Query(); q.Get("pod") != "web-1" || q.Get("filter") != "err" || len(q["match"]) != 2 {
			t.Errorf("query = %v", q)
		}
		if strings.Contains(r.URL.RawQuery, "tok-1") {
			t.Error("token in the URL")
		}
		if ua := r.Header.Get("User-Agent"); ua != "devlyft/test" {
			t.Errorf("User-Agent = %q", ua)
		}
		acceptAndSend(t, w, r, []string{
			`{"timestamp":"2026-10-25T12:00:00.5Z","line":"hello","pod":"web-1"}`,
		}, CloseReauth)
	})

	var logs bytes.Buffer
	c, err := Dial(t.Context(), &DialOptions{
		BaseURL:   srv.URL,
		DeployID:  deployID,
		Pod:       "web-1",
		Filter:    "err",
		Match:     []string{"level=error", "service=~api"},
		Token:     func(context.Context) (string, error) { return "tok-1", nil },
		UserAgent: "devlyft/test",
		Logger:    slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	defer func() { _ = c.Close() }()

	e, err := c.Read(t.Context())
	if err != nil || e.Line != "hello" || *e.Pod != "web-1" || e.Timestamp != "2026-10-25T12:00:00.5Z" {
		t.Fatalf("Read() = %+v, %v", e, err)
	}
	if _, err := c.Read(t.Context()); !errors.Is(err, ErrReauth) {
		t.Errorf("Read() after 4001 = %v, want ErrReauth", err)
	}
	if strings.Contains(logs.String(), "tok-1") {
		t.Errorf("debug log leaked the token:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "websocket dial") {
		t.Errorf("no debug log of the dial:\n%s", logs.String())
	}
}

func TestReadOtherCloseIsNotReauth(t *testing.T) {
	t.Parallel()

	srv := wsServer(t, func(w http.ResponseWriter, r *http.Request) {
		acceptAndSend(t, w, r, []string{"not json"}, websocket.StatusGoingAway)
	})
	c, err := Dial(t.Context(), &DialOptions{BaseURL: srv.URL, DeployID: deployID, Token: staticToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("Read() = %v, want a malformed-message error", err)
	}
	if _, err := c.Read(t.Context()); err == nil || errors.Is(err, ErrReauth) {
		t.Errorf("Read() = %v, want a non-reauth close", err)
	}
	_ = c.Close()
}

func TestReadSkipsBinaryFrames(t *testing.T) {
	t.Parallel()

	srv := wsServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"bearer"}})
		if err != nil {
			return
		}
		_ = c.Write(r.Context(), websocket.MessageBinary, []byte{1, 2})
		b, _ := json.Marshal(Entry{Timestamp: "2026-10-25T12:00:00Z", Line: "text"})
		_ = c.Write(r.Context(), websocket.MessageText, b)
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	c, err := Dial(t.Context(), &DialOptions{BaseURL: srv.URL, DeployID: deployID, Token: staticToken})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := c.Read(t.Context()); err != nil || e.Line != "text" {
		t.Errorf("Read() = %+v, %v", e, err)
	}
}

func TestDialRejectedHandshake(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		body   string
		exit   int
	}{
		{http.StatusUnauthorized, "unauthorized\n", exitcode.Auth},
		{http.StatusForbidden, "forbidden\n", exitcode.Auth},
		{http.StatusNotFound, "not found\n", exitcode.NotFound},
		{http.StatusBadRequest, "invalid field filter: bad\n", exitcode.Error},
		{http.StatusServiceUnavailable, "", exitcode.Error},
	}
	for _, tt := range tests {
		srv := wsServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Request-Id", "req-7")
			http.Error(w, strings.TrimSuffix(tt.body, "\n"), tt.status)
		})
		_, err := Dial(t.Context(), &DialOptions{BaseURL: srv.URL, DeployID: deployID, Token: staticToken})
		e, ok := errors.AsType[*clierr.Error](err)
		if !ok {
			t.Errorf("%d: Dial() = %v, want *clierr.Error", tt.status, err)
			continue
		}
		if e.HTTPStatus != tt.status || e.ExitCode() != tt.exit || e.RequestID != "req-7" {
			t.Errorf("%d: got status %d exit %d request %q", tt.status, e.HTTPStatus, e.ExitCode(), e.RequestID)
		}
		if !strings.HasPrefix(e.Message, "log stream: ") {
			t.Errorf("%d: message %q", tt.status, e.Message)
		}
	}
}

func TestDialNetworkAndTokenErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := Dial(t.Context(), &DialOptions{BaseURL: url, DeployID: deployID, Token: staticToken}); err == nil ||
		!strings.Contains(err.Error(), "connecting to the log stream") {
		t.Errorf("Dial() to a closed server = %v", err)
	}

	boom := errors.New("not logged in")
	if _, err := Dial(t.Context(), &DialOptions{
		BaseURL: "https://api.example.com", DeployID: deployID,
		Token: func(context.Context) (string, error) { return "", boom },
	}); !errors.Is(err, boom) {
		t.Errorf("Dial() with a failing token source = %v", err)
	}
}

func TestStreamURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base, want string
		wantErr    bool
	}{
		{base: "https://api.devlyft.io", want: "wss://api.devlyft.io/v1/deploys/" + deployID + "/logs/stream"},
		{base: "https://api.devlyft.io/", want: "wss://api.devlyft.io/v1/deploys/" + deployID + "/logs/stream"},
		{base: "http://127.0.0.1:8080", want: "ws://127.0.0.1:8080/v1/deploys/" + deployID + "/logs/stream"},
		{base: "ftp://example.com", wantErr: true},
		{base: "::", wantErr: true},
	}
	for _, tt := range tests {
		got, err := StreamURL(&DialOptions{BaseURL: tt.base, DeployID: deployID})
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("StreamURL(%q) = %q, %v", tt.base, got, err)
		}
	}
}

func staticToken(context.Context) (string, error) { return "tok", nil }
