package cli_test

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/testutil/clitest"
	"github.com/devlyft/cli/internal/testutil/golden"
)

// cleanScopeEnv clears every variable that feeds the scope, so the
// developer's shell cannot change the results.
var cleanScopeEnv = map[string]string{
	"DEVLYFT_CONTEXT":   "",
	"DEVLYFT_API_URL":   "",
	"DEVLYFT_CA_BUNDLE": "",
	"DEVLYFT_ORG":       "",
	"DEVLYFT_PROJECT":   "",
	"DEVLYFT_ENV":       "",
	"DEVLYFT_LOCATION":  "",
	"DEVLYFT_API_TOKEN": "",
	"DEVLYFT_DEBUG":     "",
	"NO_COLOR":          "",
}

// session runs several commands against one config directory and
// collects their transcripts, with the temporary path replaced.
type session struct {
	t      *testing.T
	dir    string
	env    map[string]string
	config string
	out    strings.Builder
}

func newSession(t *testing.T) *session {
	t.Helper()
	return &session{t: t, dir: t.TempDir(), env: cleanScopeEnv}
}

func (s *session) run(args ...string) clitest.Result {
	s.t.Helper()
	res := clitest.Run(s.t, &clitest.Options{Args: args, Env: s.env, ConfigDir: s.dir, Config: s.config})
	s.config = ""
	out := strings.ReplaceAll(res.Transcript(), s.dir, "$CONFIG_DIR")
	s.out.WriteString(strings.ReplaceAll(out, `$CONFIG_DIR\`, "$CONFIG_DIR/"))
	s.out.WriteString("\n")
	return res
}

func (s *session) file() string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, "config.yaml"))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

//nolint:paralleltest // clitest sets environment variables
func TestConfigAndContexts(t *testing.T) {
	s := newSession(t)

	s.run("context", "list")
	s.run("context", "use", "prod", "--org", "acme", "--project", "web", "--environment", "production", "--account", "work")
	s.run("context", "use", "staging", "--org", "acme-staging", "--location", "eu-west-1")
	s.run("context", "list")
	s.run("context", "show")
	s.run("context", "show", "--context", "prod", "--project", "api")
	s.run("context", "show", "prod")
	s.run("context", "use", "prod", "--project", "")
	s.run("context", "show", "prod", "--format", "json")
	s.run("context", "show", "nope")
	s.run("context", "delete", "staging")
	s.run("context", "delete", "staging", "--yes")
	s.run("context", "delete", "nope", "--yes")
	s.run("context", "use", "")

	s.run("config", "list")
	s.run("config", "set", "api_url", "https://api.example.com/")
	s.run("config", "get", "api_url")
	s.run("config", "set", "api_url", "http://insecure.example.com")
	s.run("config", "set", "current_context", "nope")
	s.run("config", "set", "colour", "on")
	s.run("config", "get", "colour")
	s.run("config", "get", "current_context", "--format", "json")
	s.run("context", "show")

	s.out.WriteString("--- config.yaml\n" + s.file())
	golden.Assert(t, filepath.Join("testdata", "config_contexts.golden"), s.out.String())
}

//nolint:paralleltest // clitest sets environment variables
func TestScopeErrors(t *testing.T) {
	s := newSession(t)

	// A current_context that names a missing context blocks commands that
	// need the scope, but config and context commands can still repair it.
	s.config = "current_context: gone\n"
	s.run("auth", "status")
	s.run("context", "show")
	s.run("config", "set", "current_context", "")
	s.run("context", "show")

	s.run("auth", "status", "--context", "nope")
	s.env = merge(cleanScopeEnv, map[string]string{"DEVLYFT_CONTEXT": "nope"})
	s.run("auth", "status")
	s.env = merge(cleanScopeEnv, map[string]string{"DEVLYFT_API_URL": "http://api.example.com"})
	s.run("auth", "status")
	s.env = cleanScopeEnv
	s.run("auth", "status", "--api-url", "not a url")

	// A broken file fails commands that read it, but not version.
	s.config = "contexts: [unclosed\n"
	res := s.run("context", "list")
	if res.Exit != exitcode.Error {
		t.Errorf("broken config exit = %d", res.Exit)
	}
	if res := s.run("completion", "bash"); res.Exit != exitcode.OK {
		t.Errorf("completion with broken config exit = %d", res.Exit)
	}

	out := s.out.String()
	// Drop the completion script itself from the transcript.
	if i := strings.Index(out, "$ devlyft completion bash"); i >= 0 {
		out = out[:i]
	}
	golden.Assert(t, filepath.Join("testdata", "config_scope_errors.golden"), out)
}

// The --context flag and DEVLYFT_* variables reach the account that
// credentials are looked up under.
//
//nolint:paralleltest // clitest sets environment variables
func TestContextAccount(t *testing.T) {
	s := newSession(t)
	s.config = "contexts:\n  work: {account: work}\n"
	res := s.run("auth", "status", "--context", "work")
	if res.Exit != exitcode.Auth || !strings.Contains(res.Stderr, "not logged in") {
		t.Errorf("unexpected result %+v", res)
	}
}

func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// pingCmd fetches a URL with the factory's HTTP client.
func pingCmd(f *cmdutil.Factory) []*cobra.Command {
	return []*cobra.Command{{
		Use:  "ping URL",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.HTTPClient()
			if err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, args[0], http.NoBody)
			if err != nil {
				return err
			}
			resp, err := c.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			_, err = io.Copy(cmd.OutOrStdout(), resp.Body)
			return err
		},
	}}
}

//nolint:paralleltest // clitest sets environment variables
func TestCABundleNotice(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "pong")
	}))
	t.Cleanup(srv.Close)
	bundle := filepath.Join(t.TempDir(), "corp-ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	res := clitest.Run(t, &clitest.Options{Args: []string{"ping", srv.URL}, Env: cleanScopeEnv, Commands: pingCmd})
	if res.Exit == exitcode.OK || strings.Contains(res.Stderr, "Notice") {
		t.Errorf("without a bundle: %+v", res)
	}

	res = clitest.Run(t, &clitest.Options{
		Args:     []string{"ping", srv.URL, "--quiet"},
		Env:      merge(cleanScopeEnv, map[string]string{"DEVLYFT_CA_BUNDLE": bundle}),
		Commands: pingCmd,
	})
	want := "Notice: trusting extra CA certificates from " + bundle + " (DEVLYFT_CA_BUNDLE).\n"
	if res.Exit != exitcode.OK || res.Stdout != "pong" || res.Stderr != want {
		t.Errorf("with DEVLYFT_CA_BUNDLE: %+v", res)
	}

	res = clitest.Run(t, &clitest.Options{
		Args:     []string{"ping", srv.URL},
		Env:      cleanScopeEnv,
		Config:   "ca_bundle: " + bundle + "\n",
		Commands: pingCmd,
	})
	if res.Exit != exitcode.OK || !strings.Contains(res.Stderr, "(ca_bundle in the config file)") {
		t.Errorf("with ca_bundle: %+v", res)
	}

	res = clitest.Run(t, &clitest.Options{
		Args:     []string{"ping", srv.URL},
		Env:      merge(cleanScopeEnv, map[string]string{"DEVLYFT_CA_BUNDLE": filepath.Join(t.TempDir(), "missing.pem")}),
		Commands: pingCmd,
	})
	if res.Exit != exitcode.Usage || !strings.Contains(res.Stderr, "cannot read CA bundle") {
		t.Errorf("with a missing bundle: %+v", res)
	}
}
