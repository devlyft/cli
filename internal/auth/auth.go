// Package auth resolves the credential for a command and implements the
// login flows. Credentials are stored by package credstore.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/devlyft/cli/internal/auth/credstore"
	"github.com/devlyft/cli/internal/clierr"
)

// EnvToken is the environment variable that supplies a token directly. It
// takes precedence over stored credentials and is the way to authenticate
// in CI.
const EnvToken = "DEVLYFT_API_TOKEN" //nolint:gosec // a variable name, not a credential

// DefaultAccount is the account name credentials are stored under when the
// active context does not name one.
const DefaultAccount = "default"

// Source says where a credential came from.
type Source string

// Sources, highest precedence first.
const (
	SourceEnv     Source = "env"
	SourceKeyring Source = "keyring"
	SourceFile    Source = "file"
)

// Resolved is the credential a command will use.
type Resolved struct {
	Host    string
	Account string
	Source  Source
	Token   string
	// Organizations recorded when the credential was stored; empty for
	// DEVLYFT_API_TOKEN.
	Organizations []credstore.Organization
}

// Resolver finds the credential for an API host.
type Resolver struct {
	Getenv func(string) string
	// Store opens the credential store. It is not called when
	// DEVLYFT_API_TOKEN is set, so the keyring is never touched then.
	Store func() (credstore.Store, error)
	// Account selects the stored credential; empty means DefaultAccount.
	Account string
}

func (r *Resolver) account() string {
	if r.Account != "" {
		return r.Account
	}
	return DefaultAccount
}

// Resolve returns the credential for host: DEVLYFT_API_TOKEN, then the
// stored credential. With neither it returns an auth_required error.
func (r *Resolver) Resolve(ctx context.Context, host string) (*Resolved, error) {
	if tok := r.Getenv(EnvToken); tok != "" {
		return &Resolved{Host: host, Source: SourceEnv, Token: tok}, nil
	}
	store, err := r.Store()
	if err != nil {
		return nil, err
	}
	c, err := store.Get(ctx, host, r.account())
	if errors.Is(err, credstore.ErrNotFound) {
		return nil, NotLoggedIn(host)
	}
	if uerr, ok := errors.AsType[*credstore.UnavailableError](err); ok {
		e := clierr.New(clierr.CodeAuthRequired, "cannot read the stored credential for %s: %v", host, uerr)
		e.Hint = "Unlock the system keyring, or log in again with `devlyft auth login --with-token --insecure-storage`."
		e.Err = err
		return nil, e
	}
	if err != nil {
		return nil, err
	}
	src := SourceFile
	if c.Store == credstore.KindKeyring {
		src = SourceKeyring
	}
	return &Resolved{
		Host:          host,
		Account:       c.Account,
		Source:        src,
		Token:         c.Token,
		Organizations: c.Organizations,
	}, nil
}

// NotLoggedIn is the error for a host with no credential.
func NotLoggedIn(host string) *clierr.Error {
	e := clierr.New(clierr.CodeAuthRequired, "not logged in to %s", host)
	e.Hint = fmt.Sprintf("Run `devlyft auth login --with-token`, or set %s.", EnvToken)
	return e
}

// Hint is the hint for an authentication failure from the API: the token
// in use was rejected.
func Hint(getenv func(string) string) string {
	if getenv(EnvToken) != "" {
		return EnvToken + " is invalid or expired."
	}
	return "Run `devlyft auth login --with-token` to log in again."
}

// TokenSource resolves the credential on first use and then reuses it. It
// implements api.TokenSource.
type TokenSource struct {
	Resolver *Resolver
	Host     string

	once  sync.Once
	token string
	err   error
}

// Token returns the bearer token for the host.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.once.Do(func() {
		var r *Resolved
		r, t.err = t.Resolver.Resolve(ctx, t.Host)
		if t.err == nil {
			t.token = r.Token
		}
	})
	return t.token, t.err
}
