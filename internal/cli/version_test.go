package cli

import (
	"regexp"
	"testing"

	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
)

// The version line is read by scripts and bug reports, so its shape is
// pinned: "devlyft <version>[ (<commit>[, <date>])] <go version> <os>/<arch>".
var versionLine = regexp.MustCompile(`^devlyft \S+( \([0-9a-f]{1,12}(, \S+)?\))? go\S+ [a-z0-9]+/[a-z0-9]+\n$`)

func TestVersionLineFormat(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := iostreams.Test()
	if code := Execute(t.Context(), ios, []string{"version"}); code != exitcode.OK {
		t.Fatalf("exit code = %d, want %d (stderr: %q)", code, exitcode.OK, stderr.String())
	}
	if !versionLine.MatchString(stdout.String()) {
		t.Errorf("version line %q does not match %s", stdout.String(), versionLine)
	}
}

func TestVersionRejectsArgs(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := iostreams.Test()
	if code := Execute(t.Context(), ios, []string{"version", "extra"}); code != exitcode.Usage {
		t.Fatalf("exit code = %d, want %d for unexpected argument", code, exitcode.Usage)
	}
}
