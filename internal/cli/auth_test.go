package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/api"
	apimock "github.com/devlyft/cli/internal/api/mock"
	"github.com/devlyft/cli/internal/auth/credstore"
	storemock "github.com/devlyft/cli/internal/auth/credstore/mock"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
	"github.com/devlyft/cli/internal/testutil/golden"
)

//nolint:gosec // fake tokens
const (
	testHost  = "api.devlyft.io"
	testToken = "dvl_0123456789abcdefghijklmnopqrstuvwxyzABCDE"
	envToken  = "dvl_ENVENVENVENVENVENVENVENVENVENVENVENVENVENV"
)

var acme = api.Organization{Id: uuid.MustParse("0b1c2d3e-0000-4000-8000-000000000001"), Slug: "acme", Name: "Acme"}

var stored = credstore.Credential{
	Host:          testHost,
	Account:       "default",
	Store:         credstore.KindKeyring,
	Token:         testToken,
	Organizations: []credstore.Organization{{ID: acme.Id.String(), Slug: "acme", Name: "Acme"}},
	CreatedAt:     time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
}

type authCase struct {
	name  string
	args  []string
	stdin string
	tty   bool
	env   map[string]string
	setup func(s *storemock.MockStore, c *apimock.MockClient)
	// wantToken is the token the API client must be built with.
	wantToken string
}

func hasToken(token string) gomock.Matcher {
	return gomock.Cond(func(c *credstore.Credential) bool {
		return c.Token == token && c.Host == testHost && c.Account == "default" && len(c.Organizations) == 1
	})
}

func setStore(kind credstore.Kind) func(context.Context, *credstore.Credential, bool) error {
	return func(_ context.Context, c *credstore.Credential, _ bool) error {
		c.Store = kind
		return nil
	}
}

func rejected401(context.Context) ([]api.Organization, error) {
	return nil, clierr.FromHTTP(http.StatusUnauthorized, "invalid token", "req-401")
}

//nolint:paralleltest // uses t.Setenv
func TestAuthGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("DEVLYFT_DEBUG", "")

	withEnv := map[string]string{"DEVLYFT_API_TOKEN": envToken}
	orgs := func(c *apimock.MockClient) {
		c.EXPECT().ListOrganizations(gomock.Any()).Return([]api.Organization{acme}, nil)
	}

	tests := []authCase{
		// login
		{
			name: "auth_login_with_token", args: []string{"auth", "login", "--with-token"}, stdin: "  " + testToken + "\n",
			wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				orgs(c)
				s.EXPECT().Set(gomock.Any(), hasToken(testToken), false).DoAndReturn(setStore(credstore.KindKeyring))
			},
		},
		{
			name: "auth_login_with_token_json", args: []string{"auth", "login", "--with-token", "--format", "json"}, stdin: testToken,
			wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				orgs(c)
				s.EXPECT().Set(gomock.Any(), hasToken(testToken), false).DoAndReturn(setStore(credstore.KindKeyring))
			},
		},
		{
			name: "auth_login_insecure_storage", args: []string{"auth", "login", "--with-token", "--insecure-storage"}, stdin: testToken,
			wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				orgs(c)
				s.EXPECT().Set(gomock.Any(), hasToken(testToken), true).DoAndReturn(setStore(credstore.KindFile))
			},
		},
		{
			name: "auth_login_rejected", args: []string{"auth", "login", "--with-token"}, stdin: testToken, wantToken: testToken,
			setup: func(_ *storemock.MockStore, c *apimock.MockClient) {
				c.EXPECT().ListOrganizations(gomock.Any()).DoAndReturn(rejected401)
			},
		},
		{
			name: "auth_login_rejected_json", args: []string{"auth", "login", "--with-token", "--format", "json"}, stdin: testToken, wantToken: testToken,
			setup: func(_ *storemock.MockStore, c *apimock.MockClient) {
				c.EXPECT().ListOrganizations(gomock.Any()).DoAndReturn(rejected401)
			},
		},
		{
			name: "auth_login_unreachable", args: []string{"auth", "login", "--with-token"}, stdin: testToken, wantToken: testToken,
			setup: func(_ *storemock.MockStore, c *apimock.MockClient) {
				c.EXPECT().ListOrganizations(gomock.Any()).Return(nil, clierr.New(clierr.CodeUnavailable, "cannot reach %s", testHost))
			},
		},
		{name: "auth_login_no_flag", args: []string{"auth", "login"}},
		{name: "auth_login_tty", args: []string{"auth", "login", "--with-token"}, tty: true},
		{name: "auth_login_env_set", args: []string{"auth", "login", "--with-token"}, stdin: testToken, env: withEnv},
		{name: "auth_login_empty", args: []string{"auth", "login", "--with-token"}, stdin: " \n"},
		{name: "auth_login_two_tokens", args: []string{"auth", "login", "--with-token"}, stdin: "dvl_a\ndvl_b\n"},
		{name: "auth_login_token_flag", args: []string{"auth", "login", "--token", testToken}},

		// status
		{
			name: "auth_status_keyring", args: []string{"auth", "status", "--format", "table"}, wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(&stored, nil)
				orgs(c)
			},
		},
		{
			name: "auth_status_json", args: []string{"auth", "status", "--format", "json"}, wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(&stored, nil)
				orgs(c)
			},
		},
		{
			name: "auth_status_env", args: []string{"auth", "status", "--format", "table"}, env: withEnv, wantToken: envToken,
			setup: func(_ *storemock.MockStore, c *apimock.MockClient) { orgs(c) },
		},
		{
			name: "auth_status_not_logged_in", args: []string{"auth", "status"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(nil, credstore.ErrNotFound)
			},
		},
		{
			name: "auth_status_rejected_stored", args: []string{"auth", "status"}, wantToken: testToken,
			setup: func(s *storemock.MockStore, c *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(&stored, nil)
				c.EXPECT().ListOrganizations(gomock.Any()).DoAndReturn(rejected401)
			},
		},
		{
			name: "auth_status_rejected_env", args: []string{"auth", "status"}, env: withEnv, wantToken: envToken,
			setup: func(_ *storemock.MockStore, c *apimock.MockClient) {
				c.EXPECT().ListOrganizations(gomock.Any()).DoAndReturn(rejected401)
			},
		},

		// token
		{
			name: "auth_token", args: []string{"auth", "token"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(&stored, nil)
			},
		},
		{name: "auth_token_env", args: []string{"auth", "token"}, env: withEnv},
		{
			name: "auth_token_not_logged_in", args: []string{"auth", "token"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().Get(gomock.Any(), testHost, "default").Return(nil, credstore.ErrNotFound)
			},
		},

		// logout
		{
			name: "auth_logout_yes", args: []string{"auth", "logout", "--yes"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
				s.EXPECT().Delete(gomock.Any(), testHost, "default").Return(nil)
			},
		},
		{
			name: "auth_logout_tty_confirmed", args: []string{"auth", "logout"}, tty: true, stdin: "y\n",
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
				s.EXPECT().Delete(gomock.Any(), testHost, "default").Return(nil)
			},
		},
		{
			name: "auth_logout_tty_declined", args: []string{"auth", "logout"}, tty: true, stdin: "n\n",
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
			},
		},
		{
			name: "auth_logout_needs_yes", args: []string{"auth", "logout"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
			},
		},
		{
			name: "auth_logout_env_still_set", args: []string{"auth", "logout", "--yes"}, env: withEnv,
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
				s.EXPECT().Delete(gomock.Any(), testHost, "default").Return(nil)
			},
		},
		{
			name: "auth_logout_json", args: []string{"auth", "logout", "--yes", "--format", "json"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
				s.EXPECT().Delete(gomock.Any(), testHost, "default").Return(nil)
			},
		},
		{
			name: "auth_logout_not_logged_in", args: []string{"auth", "logout", "--yes"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return(nil, nil)
			},
		},
		{
			name: "auth_logout_only_env", args: []string{"auth", "logout", "--yes"}, env: withEnv,
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return(nil, nil)
			},
		},

		// list
		{
			name: "auth_list", args: []string{"auth", "list", "--format", "table"}, env: withEnv,
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				other := stored
				other.Host, other.Store, other.Token, other.Organizations = "api.staging.example.com", credstore.KindFile, "", nil
				keyring := stored
				keyring.Token = ""
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{keyring, other}, nil)
			},
		},
		{
			name: "auth_list_json", args: []string{"auth", "list", "--format", "json"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				keyring := stored
				keyring.Token = ""
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{keyring}, nil)
			},
		},
		{
			name: "auth_list_empty_json", args: []string{"auth", "list", "--format", "json"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return(nil, nil)
			},
		},
		{
			name: "auth_list_quiet", args: []string{"auth", "ls", "-q"},
			setup: func(s *storemock.MockStore, _ *apimock.MockClient) {
				s.EXPECT().List(gomock.Any()).Return([]credstore.Credential{stored}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := storemock.NewMockStore(ctrl)
			client := apimock.NewMockClient(ctrl)
			if tt.setup != nil {
				tt.setup(store, client)
			}

			ios, in, stdout, stderr := iostreams.Test()
			ios.SetTTY(tt.tty)
			in.WriteString(tt.stdin)

			var clientTokens []string
			f := &cmdutil.Factory{
				IO:          ios,
				ConfigDir:   t.TempDir(),
				Getenv:      func(k string) string { return tt.env[k] },
				Credentials: func() (credstore.Store, error) { return store, nil },
				NewAPIClient: func(ts api.TokenSource) (api.Client, error) {
					tok, err := ts.Token(t.Context())
					if err != nil {
						return nil, err
					}
					clientTokens = append(clientTokens, tok)
					return client, nil
				},
			}
			code := execute(t.Context(), f, NewRootCmd(f), tt.args)

			for _, tok := range clientTokens {
				if tok != tt.wantToken {
					t.Errorf("API client built with token %q, want %q", tok, tt.wantToken)
				}
			}
			got := fmt.Sprintf("$ devlyft %s\n--- exit %d\n--- stdout\n%s--- stderr\n%s",
				strings.Join(tt.args, " "), code, stdout.String(), stderr.String())
			golden.Assert(t, filepath.Join("testdata", tt.name+".golden"), got)
		})
	}
}

// Whatever happens, the token never reaches stderr or debug logs, and only
// reaches stdout through `auth token`.
//
//nolint:paralleltest // uses t.Setenv
func TestAuthNeverLeaksToken(t *testing.T) {
	t.Setenv("DEVLYFT_DEBUG", "")

	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("request without Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = fmt.Fprintf(w, `{"organizations":[{"id":%q,"slug":"acme","name":"Acme","created_at":"2026-01-01T00:00:00Z"}]}`, acme.Id)
			return
		}
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	env := credstore.Env{
		Getenv:     func(k string) string { return map[string]string{"CI": "true"}[k] },
		FileExists: func(string) bool { return false },
		GOOS:       "linux",
	}

	run := func(vars map[string]string, stdin string, args ...string) (code int, stdout, stderr string) {
		ios, in, out, errOut := iostreams.Test()
		in.WriteString(stdin)
		f := &cmdutil.Factory{
			IO:        ios,
			ConfigDir: t.TempDir(),
			APIURL:    srv.URL,
			Getenv:    func(k string) string { return vars[k] },
			Credentials: func() (credstore.Store, error) {
				return credstore.New(credstore.Options{Dir: dir, Env: &env, Warn: func(string) {}}), nil
			},
		}
		code = execute(t.Context(), f, NewRootCmd(f), append(args, "--verbosity", "debug"))
		return code, out.String(), errOut.String()
	}

	for _, s := range []int{http.StatusUnauthorized, http.StatusOK} {
		status = s
		for _, format := range []string{"table", "json"} {
			code, stdout, stderr := run(nil, testToken+"\n", "auth", "login", "--with-token", "--format", format)
			if strings.Contains(stdout+stderr, testToken) {
				t.Errorf("login (HTTP %d, %s) leaked the token:\nstdout: %s\nstderr: %s", s, format, stdout, stderr)
			}
			if !strings.Contains(stderr, "[REDACTED]") {
				t.Errorf("login (HTTP %d, %s): no redacted debug log of the exchange:\n%s", s, format, stderr)
			}
			if want := map[int]int{http.StatusOK: exitcode.OK, http.StatusUnauthorized: exitcode.Auth}[s]; code != want {
				t.Errorf("login (HTTP %d) exit = %d, want %d\n%s", s, code, want, stderr)
			}

			envVars := map[string]string{"DEVLYFT_API_TOKEN": envToken}
			_, stdout, stderr = run(envVars, "", "auth", "status", "--format", format)
			if strings.Contains(stdout+stderr, envToken) {
				t.Errorf("status (HTTP %d, %s) leaked the token:\nstdout: %s\nstderr: %s", s, format, stdout, stderr)
			}
		}
	}

	// After the successful login the token is stored, and only `auth token`
	// prints it.
	code, stdout, stderr := run(nil, "", "auth", "token")
	if code != exitcode.OK || strings.TrimSpace(stdout) != testToken || strings.Contains(stderr, testToken) {
		t.Errorf("auth token: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
