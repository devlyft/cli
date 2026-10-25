package clierr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/devlyft/cli/internal/exitcode"
)

// The exit code for each error code is part of the public contract.
func TestExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want int
	}{
		{nil, exitcode.OK},
		{New(CodeInternal, "x"), exitcode.Error},
		{New(CodeUnavailable, "x"), exitcode.Error},
		{New(CodeInvalidArgument, "server-side validation"), exitcode.Error},
		{Usagef("bad flag"), exitcode.Usage},
		{New(CodeAuthRequired, "x"), exitcode.Auth},
		{New(CodePermissionDenied, "x"), exitcode.Auth},
		{New(CodeNotFound, "x"), exitcode.NotFound},
		{New(CodeConflict, "x"), exitcode.Conflict},
		{New(CodeTimeout, "x"), exitcode.Timeout},
		{New(CodePlacementPending, "x"), exitcode.PlacementPending},
		{New(CodeRolloutFailed, "x"), exitcode.RolloutFailed},
		{fmt.Errorf("wrapped: %w", New(CodeNotFound, "x")), exitcode.NotFound},
		{context.DeadlineExceeded, exitcode.Timeout},
		{fmt.Errorf("stopped: %w", context.Canceled), exitcode.Interrupted},
		{errors.New("plain"), exitcode.Error},
	}
	for _, tt := range tests {
		if got := ExitCode(tt.err); got != tt.want {
			t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestFromHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status    int
		code      Code
		exit      int
		retryable bool
	}{
		{400, CodeInvalidArgument, exitcode.Error, false},
		{401, CodeAuthRequired, exitcode.Auth, false},
		{403, CodePermissionDenied, exitcode.Auth, false},
		{404, CodeNotFound, exitcode.NotFound, false},
		{409, CodeConflict, exitcode.Conflict, false},
		{412, CodeConflict, exitcode.Conflict, false},
		{422, CodeInvalidArgument, exitcode.Error, false},
		{429, CodeUnavailable, exitcode.Error, true},
		{500, CodeUnavailable, exitcode.Error, true},
		{503, CodeUnavailable, exitcode.Error, true},
	}
	for _, tt := range tests {
		e := FromHTTP(tt.status, "", "req-1")
		if e.Code != tt.code || e.ExitCode() != tt.exit || e.Retryable != tt.retryable {
			t.Errorf("FromHTTP(%d) = code %s exit %d retryable %v, want %s %d %v",
				tt.status, e.Code, e.ExitCode(), e.Retryable, tt.code, tt.exit, tt.retryable)
		}
		if e.Message == "" || e.RequestID != "req-1" {
			t.Errorf("FromHTTP(%d) lost message or request ID: %+v", tt.status, e)
		}
	}
}

func TestWriteJSONShape(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteJSON(&buf, New(CodeNotFound, "deploy %q not found", "web")); err != nil {
		t.Fatal(err)
	}
	want := `{"code":"not_found","message":"deploy \"web\" not found","http_status":null,"request_id":null,"details":null,"retryable":false}` + "\n"
	if buf.String() != want {
		t.Errorf("got  %s\nwant %s", buf.String(), want)
	}
}
