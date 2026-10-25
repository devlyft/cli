package cli

import (
	"strings"
	"testing"

	"github.com/devlyft/cli/internal/iostreams"
)

func TestExecuteVersion(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := iostreams.Test()
	code := Execute(t.Context(), ios, []string{"version"})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "devlyft ") {
		t.Errorf("stdout = %q, want prefix %q", stdout.String(), "devlyft ")
	}
}

func TestWantsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"x", "--format", "json"}, true},
		{[]string{"x", "--format=json"}, true},
		{[]string{"x", "--jq", "."}, true},
		{[]string{"x", "--jq=."}, true},
		{[]string{"x", "--format", "yaml"}, false},
		{[]string{"x", "--", "--format", "json"}, false},
		{[]string{"x"}, false},
	}
	for _, tt := range tests {
		if got := wantsJSON(tt.args); got != tt.want {
			t.Errorf("wantsJSON(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
