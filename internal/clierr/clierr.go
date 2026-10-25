// Package clierr defines the error type every command returns and the single
// mapping from errors to stable error codes and exit codes.
package clierr

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/devlyft/cli/internal/exitcode"
)

// Code is a stable, machine-readable error code. Values are part of the
// public contract: they may be added but never changed or removed within a
// major version.
type Code string

// Error codes.
const (
	CodeAuthRequired     Code = "auth_required"
	CodePermissionDenied Code = "permission_denied"
	CodeNotFound         Code = "not_found"
	CodeConflict         Code = "conflict"
	CodeInvalidArgument  Code = "invalid_argument"
	CodeTimeout          Code = "timeout"
	CodePlacementPending Code = "placement_pending"
	CodeRolloutFailed    Code = "rollout_failed"
	CodeUnavailable      Code = "unavailable"
	CodeInternal         Code = "internal"
)

// Error is a failure the CLI reports to the user.
type Error struct {
	Code    Code
	Message string
	// Hint is an optional next step, shown to humans only.
	Hint string
	// HTTPStatus is the response status for API errors, or 0.
	HTTPStatus int
	// RequestID is the server's request ID, when it sent one.
	RequestID string
	// Details carries structured, code-specific information.
	Details map[string]any
	// Retryable reports whether running the command again may succeed.
	Retryable bool
	// Usage marks a mistake caught locally, which exits with exitcode.Usage.
	Usage bool
	// Err is the underlying cause, if any.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil && e.Message == "" {
		return e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns the process exit code for the error.
func (e *Error) ExitCode() int {
	if e.Usage {
		return exitcode.Usage
	}
	switch e.Code {
	case CodeAuthRequired, CodePermissionDenied:
		return exitcode.Auth
	case CodeNotFound:
		return exitcode.NotFound
	case CodeConflict:
		return exitcode.Conflict
	case CodeTimeout:
		return exitcode.Timeout
	case CodePlacementPending:
		return exitcode.PlacementPending
	case CodeRolloutFailed:
		return exitcode.RolloutFailed
	case CodeInvalidArgument, CodeUnavailable, CodeInternal:
		return exitcode.Error
	}
	return exitcode.Error
}

// New returns an error with the given code and message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Usagef returns a usage error: a mistake the CLI caught locally.
func Usagef(format string, args ...any) *Error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, args...), Usage: true}
}

// FromHTTP builds an error from an API response status. message should be
// the server's error message when it sent one.
func FromHTTP(status int, message, requestID string) *Error {
	e := &Error{Code: codeForStatus(status), Message: message, HTTPStatus: status, RequestID: requestID}
	e.Retryable = status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

func codeForStatus(status int) Code {
	switch {
	case status == http.StatusUnauthorized:
		return CodeAuthRequired
	case status == http.StatusForbidden:
		return CodePermissionDenied
	case status == http.StatusNotFound:
		return CodeNotFound
	case status == http.StatusConflict, status == http.StatusPreconditionFailed:
		return CodeConflict
	case status == http.StatusRequestTimeout, status == http.StatusGatewayTimeout:
		return CodeUnavailable
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		return CodeUnavailable
	case status >= http.StatusBadRequest:
		return CodeInvalidArgument
	}
	return CodeInternal
}

// IsInterrupted reports whether err was caused by the user pressing Ctrl-C.
func IsInterrupted(err error) bool {
	return errors.Is(err, context.Canceled)
}

// As converts any error into an *Error. Errors that are not already an
// *Error become CodeInternal, except a deadline, which becomes CodeTimeout.
func As(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Message: err.Error(), Err: err, Retryable: true}
	}
	return &Error{Code: CodeInternal, Message: err.Error(), Err: err}
}

// ExitCode returns the process exit code for any error; nil is exitcode.OK.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return exitcode.OK
	case IsInterrupted(err):
		return exitcode.Interrupted
	}
	return As(err).ExitCode()
}
