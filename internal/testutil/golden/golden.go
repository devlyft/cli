// Package golden compares test output with files under testdata.
//
// Run `go test ./... -update` to rewrite the files from the current output,
// then review the diff before committing.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// Assert fails tb if got differs from the contents of path. With -update it
// writes got to path instead.
func Assert(tb testing.TB, path, got string) {
	tb.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			tb.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		tb.Fatalf("reading golden file (run with -update to create): %v", err)
	}
	if got != string(want) {
		tb.Errorf("output mismatch for %s (run with -update to accept)\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}
