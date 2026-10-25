package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

func TestLoadMissingAndEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path() != filepath.Join(dir, FileName) || len(f.Contexts) != 0 {
		t.Errorf("unexpected config %+v", f)
	}

	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("\n  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Errorf("empty file: %v", err)
	}
}

func TestLoadInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("contexts: [1, 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "invalid config file") {
		t.Fatalf("err = %v, want invalid config file", err)
	}
}

func TestSaveRoundTripKeepsUnknownKeys(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "devlyft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	in := `api_url: https://api.example.com
future_setting: keep me
current_context: prod
contexts:
  prod:
    org: acme
    project: web
    future_field: 42
  empty:
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Contexts["empty"] == nil {
		t.Error("null context not normalised")
	}
	f.Contexts["prod"].Env = "production"
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, FileName)) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"future_setting: keep me", "future_field: 42", "env: production", "org: acme"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("saved config lacks %q:\n%s", want, b)
		}
	}
}

func TestSaveModes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}

	dir := filepath.Join(t.TempDir(), "new", "devlyft")
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("api_url", "https://api.example.com/"); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", st.Mode().Perm())
	}
	st, err = os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", st.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestGetSet(t *testing.T) {
	t.Parallel()

	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, testCAPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	notPEM := filepath.Join(t.TempDir(), "x.pem")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &File{Contexts: map[string]*Context{"prod": {}}}
	tests := []struct {
		key, value, want string
		exit             int
	}{
		{"api_url", "https://api.example.com/", "https://api.example.com", exitcode.OK},
		{"api_url", "http://127.0.0.1:8080", "http://127.0.0.1:8080", exitcode.OK},
		{"api_url", "http://api.example.com", "", exitcode.Usage},
		{"api_url", "api.example.com", "", exitcode.Usage},
		{"api_url", "", "", exitcode.OK},
		{"ca_bundle", ca, ca, exitcode.OK},
		{"ca_bundle", notPEM, "", exitcode.Usage},
		{"ca_bundle", filepath.Join(t.TempDir(), "missing.pem"), "", exitcode.Usage},
		{"current_context", "prod", "prod", exitcode.OK},
		{"current_context", "nope", "", exitcode.NotFound},
		{"current_context", "", "", exitcode.OK},
		{"colour", "on", "", exitcode.Usage},
	}
	for _, tt := range tests {
		err := f.Set(tt.key, tt.value)
		if got := clierr.ExitCode(err); got != tt.exit {
			t.Errorf("Set(%q, %q) exit = %d (%v), want %d", tt.key, tt.value, got, err, tt.exit)
			continue
		}
		if err != nil {
			continue
		}
		if got, _ := f.Get(tt.key); got != tt.want {
			t.Errorf("after Set(%q, %q), Get = %q, want %q", tt.key, tt.value, got, tt.want)
		}
	}
	if _, err := f.Get("colour"); err == nil {
		t.Error("Get(unknown) should fail")
	}
}

func TestHost(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://API.devlyft.io":    "api.devlyft.io",
		"https://api.devlyft.io/v1": "api.devlyft.io",
		"http://127.0.0.1:8080":     "127.0.0.1:8080",
		"not a url":                 "not a url",
	}
	for in, want := range tests {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDir(t *testing.T) {
	t.Parallel()

	dir, err := Dir()
	if err != nil {
		t.Skip("no user config dir:", err)
	}
	if filepath.Base(dir) != "devlyft" {
		t.Errorf("Dir() = %q", dir)
	}
}

func TestNoSuchContext(t *testing.T) {
	t.Parallel()

	e := NoSuchContext("prod")
	if !errors.Is(e, e) || e.ExitCode() != exitcode.NotFound || !strings.Contains(e.Hint, "context use prod") {
		t.Errorf("unexpected error %+v", e)
	}
}
