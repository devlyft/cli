package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestParseMergesAndExcludes(t *testing.T) {
	t.Parallel()

	profile := `mode: atomic
github.com/devlyft/cli/internal/a/a.go:1.1,2.1 3 0
github.com/devlyft/cli/internal/a/a.go:1.1,2.1 3 1
github.com/devlyft/cli/internal/a/a.go:3.1,4.1 1 0
github.com/devlyft/cli/internal/b/b.go:1.1,2.1 4 2
github.com/devlyft/cli/internal/api/gen/x/x.go:1.1,2.1 100 0
`
	blocks, err := parse(strings.NewReader(profile), regexp.MustCompile(`/internal/api/gen/`))
	if err != nil {
		t.Fatal(err)
	}
	total, covered, perPkg := summarise(blocks)
	if total != 8 || covered != 7 {
		t.Errorf("total, covered = %d, %d; want 8, 7", total, covered)
	}
	if got := perPkg["github.com/devlyft/cli/internal/a"]; got != [2]int{4, 3} {
		t.Errorf("package a = %v, want [4 3]", got)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	t.Parallel()

	for _, line := range []string{"x.go:1.1,2.1 3", "x.go:1.1,2.1 three 1", "x.go:1.1,2.1 3 one"} {
		if _, err := parse(strings.NewReader(line+"\n"), nil); err == nil {
			t.Errorf("parse(%q) succeeded, want error", line)
		}
	}
}

func TestPct(t *testing.T) {
	t.Parallel()

	if got := pct(0, 0); got != 100 {
		t.Errorf("pct(0, 0) = %v, want 100", got)
	}
	if got := pct(1, 4); got != 25 {
		t.Errorf("pct(1, 4) = %v, want 25", got)
	}
}
