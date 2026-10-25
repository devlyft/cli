package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
	updatecheck "github.com/devlyft/cli/internal/update"
)

func TestStartUpdateCheckDisabled(t *testing.T) {
	t.Parallel()

	// Test streams are not terminals and test builds are "dev", so the
	// notifier must not start.
	ios, _, _, _ := iostreams.Test()
	if n := startUpdateCheck(t.Context(), ios, []string{"widgets"}); n != nil {
		t.Error("update check started for a non-terminal dev build")
	}
}

func TestUpdateNotifierFinish(t *testing.T) {
	t.Parallel()

	rel := &updatecheck.Release{Version: "0.2.0", URL: "https://example.com/v0.2.0"}
	tests := []struct {
		name   string
		result *updatecheck.Release
		ready  bool
		code   int
		want   string
	}{
		{name: "newer release after success", result: rel, ready: true, code: exitcode.OK, want: "0.1.0 → 0.2.0"},
		{name: "no newer release", result: nil, ready: true, code: exitcode.OK},
		{name: "command failed", result: rel, ready: true, code: exitcode.NotFound},
		{name: "check not ready within grace", ready: false, code: exitcode.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var errOut bytes.Buffer
			n := &updateNotifier{result: make(chan *updatecheck.Release, 1), current: "0.1.0", errOut: &errOut}
			if tt.ready {
				n.result <- tt.result
			}
			n.finish(tt.code)
			if tt.want == "" && errOut.Len() != 0 {
				t.Errorf("stderr = %q, want empty", errOut.String())
			}
			if tt.want != "" && !strings.Contains(errOut.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", errOut.String(), tt.want)
			}
		})
	}

	// A nil notifier (disabled) is a no-op.
	var n *updateNotifier
	n.finish(exitcode.OK)
}

func TestFirstArg(t *testing.T) {
	t.Parallel()

	for args, want := range map[string]string{
		"version":            "version",
		"--no-color version": "version",
		"completion bash":    "completion",
		"-- version":         "",
		"":                   "",
	} {
		if got := firstArg(strings.Fields(args)); got != want {
			t.Errorf("firstArg(%q) = %q, want %q", args, got, want)
		}
	}
	if !skipUpdateCommands["version"] || !skipUpdateCommands["completion"] || skipUpdateCommands["deploys"] {
		t.Error("unexpected skip list")
	}
}
