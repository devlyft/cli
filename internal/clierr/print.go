package clierr

import (
	"encoding/json"
	"fmt"
	"io"
)

// jsonError is the shape of an error under --format json. Every field is
// always present so scripts can rely on it.
type jsonError struct {
	Code       Code           `json:"code"`
	Message    string         `json:"message"`
	HTTPStatus *int           `json:"http_status"`
	RequestID  *string        `json:"request_id"`
	Details    map[string]any `json:"details"`
	Retryable  bool           `json:"retryable"`
}

// WriteJSON writes err to w as a single-line JSON object.
func WriteJSON(w io.Writer, err *Error) error {
	je := jsonError{
		Code:      err.Code,
		Message:   err.Error(),
		Details:   err.Details,
		Retryable: err.Retryable,
	}
	if err.HTTPStatus != 0 {
		je.HTTPStatus = &err.HTTPStatus
	}
	if err.RequestID != "" {
		je.RequestID = &err.RequestID
	}
	return json.NewEncoder(w).Encode(je)
}

// WriteHuman writes err to w as a summary line, then an optional hint and
// request ID.
func WriteHuman(w io.Writer, err *Error, color bool) error {
	prefix := "Error:"
	if color {
		prefix = "\x1b[1;31mError:\x1b[0m"
	}
	if _, werr := fmt.Fprintf(w, "%s %s\n", prefix, err.Error()); werr != nil {
		return werr
	}
	if err.Hint != "" {
		if _, werr := fmt.Fprintf(w, "Hint: %s\n", err.Hint); werr != nil {
			return werr
		}
	}
	if err.RequestID != "" {
		if _, werr := fmt.Fprintf(w, "Request ID: %s\n", err.RequestID); werr != nil {
			return werr
		}
	}
	return nil
}
