package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/devlyft/cli/internal/clierr"
)

// expect returns v for a 2xx response, and the API error otherwise.
func expect[T any](resp *http.Response, body []byte, v *T, attempts int) (*T, error) {
	if err := checkStatus(resp, body, attempts); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, clierr.New(clierr.CodeInternal, "unexpected %s response from the API", resp.Status)
	}
	return v, nil
}

// checkStatus returns nil for a 2xx response and an API error otherwise.
func checkStatus(resp *http.Response, body []byte, attempts int) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return apiError(resp, body, attempts)
}

// apiError maps a non-2xx response to a *clierr.Error carrying the server's
// message and request ID.
func apiError(resp *http.Response, body []byte, attempts int) *clierr.Error {
	msg, requestID := parseErrorBody(body)
	if h := resp.Header.Get("X-Request-Id"); h != "" {
		requestID = h
	}
	e := clierr.FromHTTP(resp.StatusCode, msg, requestID)
	if attempts > 1 {
		e.Message += fmt.Sprintf(" (after %d attempts)", attempts)
		e.Details = map[string]any{"attempts": attempts}
	}
	return e
}

// parseErrorBody extracts a message and request ID from an error body. The
// platform's services use a few shapes (RFC 7807 problem details, {error},
// {message}); a short plain-text body is used as is.
func parseErrorBody(body []byte) (msg, requestID string) {
	var fields map[string]any
	if json.Unmarshal(body, &fields) == nil {
		for _, k := range []string{"detail", "message", "error", "title"} {
			if s, ok := fields[k].(string); ok && s != "" {
				msg = s
				break
			}
		}
		for _, k := range []string{"request_id", "requestId"} {
			if s, ok := fields[k].(string); ok && s != "" {
				requestID = s
				break
			}
		}
		return msg, requestID
	}
	text := strings.TrimSpace(string(body))
	if text != "" && len(text) <= 300 && utf8.ValidString(text) && !strings.HasPrefix(text, "<") {
		msg = text
	}
	return msg, ""
}

// transportError wraps a failure to get any response. Cancellation and
// deadlines pass through unchanged so the caller sees an interrupt or a
// timeout.
func (c *client) transportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := errors.AsType[*clierr.Error](err); ok {
		return err
	}
	return &clierr.Error{
		Code:      clierr.CodeUnavailable,
		Message:   fmt.Sprintf("cannot reach the DevLyft API at %s: %v", c.baseURL, err),
		Retryable: true,
		Err:       err,
	}
}

// parseID parses a resource ID, reporting a usage error for a bad one.
func parseID(kind, id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, clierr.Usagef("invalid %s ID %q: expected a UUID", kind, id)
	}
	return u, nil
}
