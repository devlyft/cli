package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devlyft/cli/internal/exitcode"
)

func TestExecuteVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), []string{"version"}, &stdout, &stderr)

	if code != exitcode.OK {
		t.Fatalf("exit code = %d, want %d (stderr: %q)", code, exitcode.OK, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "devlyft ") {
		t.Errorf("stdout = %q, want prefix %q", stdout.String(), "devlyft ")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), []string{"nope"}, &stdout, &stderr)

	if code != exitcode.Error {
		t.Fatalf("exit code = %d, want %d", code, exitcode.Error)
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Errorf("stderr = %q, want unknown command error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}
