package logstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/redact"
)

// CloseReauth is the close code logs-api sends when a stream reaches its
// maximum lifetime (30 minutes) or its tail ends.
const CloseReauth websocket.StatusCode = 4001

// bearerSubprotocol is the first of the two subprotocol values carrying
// the token: `Sec-WebSocket-Protocol: bearer, <token>`. Browsers cannot set
// an Authorization header on a WebSocket handshake, so logs-api reads the
// token from there instead.
const bearerSubprotocol = "bearer"

// readLimit is the largest frame accepted; one frame is one log line.
const readLimit = 1 << 20

// DialOptions describe a live-tail connection.
type DialOptions struct {
	// BaseURL is the API origin, e.g. https://api.devlyft.io.
	BaseURL  string
	DeployID string
	// Pod, Filter and Match narrow the stream as on the REST endpoint.
	Pod    string
	Filter string
	Match  []string
	// Token returns a valid bearer token; it is asked on every dial.
	Token func(ctx context.Context) (string, error)
	// HTTPClient carries proxy and CA settings; nil uses a default client.
	HTTPClient *http.Client
	UserAgent  string
	// Logger receives redacted debug logs of each handshake; nil discards.
	Logger *slog.Logger
}

// StreamURL returns the WebSocket URL for opts: wss for an https API and
// ws for a plain-http loopback API used in tests.
func StreamURL(opts *DialOptions) (string, error) {
	u, err := url.Parse(strings.TrimRight(opts.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid API URL %q", opts.BaseURL)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("invalid API URL %q", opts.BaseURL)
	}
	u.Path += "/v1/deploys/" + url.PathEscape(opts.DeployID) + "/logs/stream"
	q := url.Values{}
	if opts.Pod != "" {
		q.Set("pod", opts.Pod)
	}
	if opts.Filter != "" {
		q.Set("filter", opts.Filter)
	}
	for _, m := range opts.Match {
		q.Add("match", m)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Dial opens a live-tail connection, authenticating with a fresh token in
// the bearer subprotocol. The token never appears in the URL. A handshake
// the server rejects returns a *clierr.Error carrying its HTTP status.
func Dial(ctx context.Context, opts *DialOptions) (Conn, error) {
	streamURL, err := StreamURL(opts)
	if err != nil {
		return nil, err
	}
	token, err := opts.Token(ctx)
	if err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	header := http.Header{}
	if opts.UserAgent != "" {
		header.Set("User-Agent", opts.UserAgent)
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	start := time.Now()
	logger.Debug("websocket dial", "url", streamURL, "header", redact.Header(withProtocol(header, token)))
	c, resp, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{
		HTTPClient:   hc,
		HTTPHeader:   header,
		Subprotocols: []string{bearerSubprotocol, token},
	})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			logger.Debug("websocket rejected", "status", resp.StatusCode, "duration", time.Since(start))
			return nil, handshakeError(resp)
		}
		logger.Debug("websocket dial failed", "error", redact.String(err.Error()), "duration", time.Since(start))
		return nil, fmt.Errorf("connecting to the log stream: %w", err)
	}
	logger.Debug("websocket connected", "duration", time.Since(start))
	c.SetReadLimit(readLimit)
	return &wsConn{c: c}, nil
}

// withProtocol returns h plus the subprotocol header as sent, for logging
// through redaction.
func withProtocol(h http.Header, token string) http.Header {
	out := h.Clone()
	out.Set("Sec-WebSocket-Protocol", bearerSubprotocol+", "+token)
	return out
}

// handshakeError maps a rejected handshake to the CLI's errors. logs-api
// answers with a plain-text reason before upgrading.
func handshakeError(resp *http.Response) error {
	var msg string
	if resp.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg = strings.TrimSpace(redact.String(string(b)))
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return clierr.FromHTTP(resp.StatusCode, "log stream: "+msg, resp.Header.Get("X-Request-Id"))
}

type wsConn struct{ c *websocket.Conn }

func (w *wsConn) Read(ctx context.Context) (Entry, error) {
	for {
		typ, data, err := w.c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == CloseReauth {
				return Entry{}, fmt.Errorf("%w: %w", ErrReauth, err)
			}
			return Entry{}, err
		}
		if typ != websocket.MessageText {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			return Entry{}, fmt.Errorf("log stream: malformed message: %w", err)
		}
		return e, nil
	}
}

// Close drops the connection without waiting for a close handshake: by
// the time Follow closes a connection it has either been closed by the
// server or is being abandoned.
func (w *wsConn) Close() error {
	_ = w.c.CloseNow()
	return nil
}
