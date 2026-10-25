package auth

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/auth/credstore"
	"github.com/devlyft/cli/internal/auth/credstore/mock"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func resolver(t *testing.T, vars map[string]string, setup func(*mock.MockStore)) (*Resolver, *int) {
	t.Helper()
	store := mock.NewMockStore(gomock.NewController(t))
	if setup != nil {
		setup(store)
	}
	opened := 0
	return &Resolver{
		Getenv: env(vars),
		Store: func() (credstore.Store, error) {
			opened++
			return store, nil
		},
	}, &opened
}

func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	const host = "api.devlyft.io"
	stored := func(kind credstore.Kind, token string) func(*mock.MockStore) {
		return func(s *mock.MockStore) {
			s.EXPECT().Get(gomock.Any(), host, DefaultAccount).Return(&credstore.Credential{
				Host: host, Account: DefaultAccount, Store: kind, Token: token,
				Organizations: []credstore.Organization{{ID: "1", Name: "Acme"}},
			}, nil)
		}
	}

	t.Run("env beats stored and never opens the store", func(t *testing.T) {
		t.Parallel()
		r, opened := resolver(t, map[string]string{EnvToken: "dvl_env"}, nil)
		got, err := r.Resolve(t.Context(), host)
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "dvl_env" || got.Source != SourceEnv || *opened != 0 {
			t.Errorf("got %+v, store opened %d times", got, *opened)
		}
	})

	t.Run("keyring", func(t *testing.T) {
		t.Parallel()
		r, _ := resolver(t, nil, stored(credstore.KindKeyring, "dvl_kr"))
		got, err := r.Resolve(t.Context(), host)
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "dvl_kr" || got.Source != SourceKeyring || got.Account != DefaultAccount || len(got.Organizations) != 1 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		r, _ := resolver(t, nil, stored(credstore.KindFile, "dvl_file"))
		got, err := r.Resolve(t.Context(), host)
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "dvl_file" || got.Source != SourceFile {
			t.Errorf("got %+v", got)
		}
	})
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	const host = "api.devlyft.io"

	t.Run("not logged in", func(t *testing.T) {
		t.Parallel()
		r, _ := resolver(t, nil, func(s *mock.MockStore) {
			s.EXPECT().Get(gomock.Any(), host, DefaultAccount).Return(nil, credstore.ErrNotFound)
		})
		_, err := r.Resolve(t.Context(), host)
		e, ok := errors.AsType[*clierr.Error](err)
		if !ok || e.ExitCode() != exitcode.Auth || !strings.Contains(e.Message, host) || !strings.Contains(e.Hint, EnvToken) {
			t.Errorf("err = %#v", err)
		}
	})

	t.Run("keyring unavailable", func(t *testing.T) {
		t.Parallel()
		r, _ := resolver(t, nil, func(s *mock.MockStore) {
			s.EXPECT().Get(gomock.Any(), host, DefaultAccount).Return(nil, &credstore.UnavailableError{Reason: credstore.ReasonLocked})
		})
		_, err := r.Resolve(t.Context(), host)
		e, ok := errors.AsType[*clierr.Error](err)
		if !ok || e.ExitCode() != exitcode.Auth || !strings.Contains(e.Hint, "--insecure-storage") {
			t.Errorf("err = %#v", err)
		}
	})

	t.Run("other store error passes through", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("disk on fire")
		r, _ := resolver(t, nil, func(s *mock.MockStore) {
			s.EXPECT().Get(gomock.Any(), host, DefaultAccount).Return(nil, boom)
		})
		if _, err := r.Resolve(t.Context(), host); !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	})

	t.Run("store cannot open", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("no home")
		r := &Resolver{Getenv: env(nil), Store: func() (credstore.Store, error) { return nil, boom }}
		if _, err := r.Resolve(t.Context(), host); !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	})
}

func TestHint(t *testing.T) {
	t.Parallel()
	if h := Hint(env(map[string]string{EnvToken: "x"})); !strings.Contains(h, "invalid or expired") {
		t.Errorf("Hint with env token = %q", h)
	}
	if h := Hint(env(nil)); !strings.Contains(h, "devlyft auth login") {
		t.Errorf("Hint without env token = %q", h)
	}
}

func TestTokenSourceResolvesOnce(t *testing.T) {
	t.Parallel()
	r, opened := resolver(t, nil, func(s *mock.MockStore) {
		s.EXPECT().Get(gomock.Any(), "h", DefaultAccount).Return(&credstore.Credential{Token: "dvl_x", Store: credstore.KindFile}, nil).Times(1)
	})
	ts := &TokenSource{Resolver: r, Host: "h"}
	for range 3 {
		tok, err := ts.Token(t.Context())
		if err != nil || tok != "dvl_x" {
			t.Fatalf("Token() = %q, %v", tok, err)
		}
	}
	if *opened != 1 {
		t.Errorf("store opened %d times, want 1", *opened)
	}
}

func TestTokenSourceError(t *testing.T) {
	t.Parallel()
	r, _ := resolver(t, nil, func(s *mock.MockStore) {
		s.EXPECT().Get(gomock.Any(), "h", DefaultAccount).Return(nil, credstore.ErrNotFound)
	})
	ts := &TokenSource{Resolver: r, Host: "h"}
	if _, err := ts.Token(t.Context()); clierr.ExitCode(err) != exitcode.Auth {
		t.Errorf("Token() error = %v, want auth error", err)
	}
}
