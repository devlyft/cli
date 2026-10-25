package cli

import (
	"strings"
	"testing"

	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
)

func TestCompletion(t *testing.T) {
	t.Parallel()

	// Each script must mention the binary so it registers completions for it.
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			ios, _, stdout, stderr := iostreams.Test()
			code := Execute(t.Context(), ios, []string{"completion", shell})

			if code != exitcode.OK {
				t.Fatalf("exit code = %d, want %d (stderr: %q)", code, exitcode.OK, stderr.String())
			}
			if !strings.Contains(stdout.String(), "devlyft") {
				t.Errorf("%s script does not mention devlyft", shell)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestCompletionInvalidArgs(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unknown shell": {"completion", "tcsh"},
		"no shell":      {"completion"},
		"two shells":    {"completion", "bash", "zsh"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ios, _, stdout, _ := iostreams.Test()
			code := Execute(t.Context(), ios, args)

			if code != exitcode.Usage {
				t.Fatalf("exit code = %d, want %d", code, exitcode.Usage)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestCompletionReplacesCobraDefault(t *testing.T) {
	t.Parallel()

	ios, _, stdout, _ := iostreams.Test()
	if code := Execute(t.Context(), ios, []string{"completion", "--help"}); code != exitcode.OK {
		t.Fatalf("exit code = %d, want %d", code, exitcode.OK)
	}
	if !strings.Contains(stdout.String(), "devlyft completion powershell | Out-String") {
		t.Errorf("help text is not ours:\n%s", stdout.String())
	}
}
