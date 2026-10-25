package cli_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/devlyft/cli/internal/cli"
	"github.com/devlyft/cli/internal/iostreams"
)

// TestMain lets testscript run this test binary as the `devlyft` command,
// so scripts exercise the real entry point: process exit codes, real
// stdout and stderr, and environment variables.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"devlyft": func() {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			code := cli.Execute(ctx, iostreams.System(), os.Args[1:])
			stop()
			os.Exit(code)
		},
	})
}

// TestScripts runs testdata/script/*.txtar. Besides the standard commands,
// scripts can use:
//
//	exitcode N prog [args...]   run prog and require exit code N; its
//	                            stdout and stderr are then checked as usual
//	fakeapi                     serve $WORK/api as a fake API and point
//	                            DEVLYFT_API_URL at it: GET /v1/x is answered
//	                            with $WORK/api/GET/v1/x.json, 404 otherwise
func TestScripts(t *testing.T) {
	t.Parallel()
	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			// The update notifier is off without a terminal anyway; be explicit.
			env.Setenv("DEVLYFT_NO_UPDATE_NOTIFIER", "1")
			env.Setenv("NO_COLOR", "")
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"exitcode": cmdExitCode,
			"fakeapi":  cmdFakeAPI,
		},
	})
}

func cmdExitCode(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 {
		ts.Fatalf("usage: exitcode N prog [args...]")
	}
	want, err := strconv.Atoi(args[0])
	if err != nil {
		ts.Fatalf("exitcode: invalid code %q", args[0])
	}
	got := 0
	if err := ts.Exec(args[1], args[2:]...); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			ts.Fatalf("exitcode: %v", err)
		}
		got = exitErr.ExitCode()
	}
	if got != want {
		ts.Fatalf("%s exited with %d, want %d", strings.Join(args[1:], " "), got, want)
	}
}

func cmdFakeAPI(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: fakeapi")
	}
	srv := httptest.NewServer(fakeAPI(ts.MkAbs("api")))
	ts.Defer(srv.Close)
	ts.Setenv("DEVLYFT_API_URL", srv.URL)
}

// fakeAPI answers METHOD /path with root/METHOD/path.json.
func fakeAPI(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(root, r.Method, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))+".json")
		body, err := os.ReadFile(path) //nolint:gosec // test fixture under $WORK
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body) //nolint:gosec // test fixture, not user input
	})
}

func TestFakeAPI(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "GET", "v1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "organizations.json"), []byte(`{"organizations":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := fakeAPI(root)

	tests := []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodGet, "/v1/organizations", http.StatusOK, `{"organizations":[]}`},
		{http.MethodPost, "/v1/organizations", http.StatusNotFound, ""},
		{http.MethodGet, "/v1/projects", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody))
		if rec.Code != tt.status {
			t.Errorf("%s %s: status %d, want %d", tt.method, tt.path, rec.Code, tt.status)
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("%s %s: body %q, want %q", tt.method, tt.path, rec.Body.String(), tt.body)
		}
	}
}
