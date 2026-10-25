// Package cmdutil holds what every command needs: streams, the result
// printer and the debug logger. Command packages receive a *Factory when
// they are constructed; its fields are ready by the time RunE runs.
package cmdutil

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/auth"
	"github.com/devlyft/cli/internal/auth/credstore"
	"github.com/devlyft/cli/internal/config"
	"github.com/devlyft/cli/internal/iostreams"
	"github.com/devlyft/cli/internal/output"
)

// Factory carries shared dependencies to commands. The root command fills
// any field left nil; tests set them first to substitute fakes.
type Factory struct {
	IO      *iostreams.IOStreams
	Printer *output.Printer
	Log     *slog.Logger

	// ConfigDir holds config.yaml and credentials.json; empty means
	// config.Dir().
	ConfigDir string
	// Config is the loaded config file. Nil for commands that do not
	// read it (version, completion).
	Config *config.File
	// Scope is the resolved API URL, context and defaults. Nil for
	// commands that do not need it (version, completion, config, context).
	Scope *config.Scope

	// APIURL is the API origin from the scope.
	APIURL string
	// Getenv reads the environment.
	Getenv func(string) string
	// Credentials opens the credential store.
	Credentials func() (credstore.Store, error)
	// HTTPClient returns the client every network call uses, with the
	// proxy and CA bundle settings applied. Use it for the API and for
	// anything else that dials out, such as the log stream.
	HTTPClient func() (*http.Client, error)
	// NewAPIClient returns an API client for APIURL using tokens.
	NewAPIClient func(tokens api.TokenSource) (api.Client, error)

	// API, when set, overrides Client. Tests use it to inject the gomock
	// client from internal/api/mock.
	API func() (api.Client, error)
}

// Host is the API host credentials are stored under.
func (f *Factory) Host() string { return config.Host(f.APIURL) }

// Account is the account credentials are stored under: the active
// context's account, or auth.DefaultAccount.
func (f *Factory) Account() string {
	if f.Scope != nil && f.Scope.Account.Value != "" {
		return f.Scope.Account.Value
	}
	return auth.DefaultAccount
}

// Resolver finds the credential for a command.
func (f *Factory) Resolver() *auth.Resolver {
	return &auth.Resolver{Getenv: f.Getenv, Store: f.Credentials, Account: f.Account()}
}

// APIClient returns an API client authenticated with the resolved
// credential. The credential is resolved on the first request.
func (f *Factory) APIClient() (api.Client, error) {
	return f.NewAPIClient(&auth.TokenSource{Resolver: f.Resolver(), Host: f.Host()})
}

// YesFlagUsage is the help text for the --yes flag on destructive commands.
const YesFlagUsage = "Skip the confirmation prompt (required when not running interactively)"

// ErrNoAPI is returned by Client when no API client has been configured.
var ErrNoAPI = errors.New("no API client configured")

// Client returns the API client commands should use: API when a test has
// set it, otherwise one authenticated with the resolved credential.
func (f *Factory) Client() (api.Client, error) {
	if f.API != nil {
		return f.API()
	}
	if f.NewAPIClient == nil {
		return nil, ErrNoAPI
	}
	return f.APIClient()
}

// Annotations on commands that change how the root sets them up.
const (
	// NoConfig skips loading the config file and resolving the scope.
	NoConfig = "devlyft/no-config"
	// NoScope loads the config file but does not resolve the scope, so
	// that config and context commands can repair a broken setting.
	NoScope = "devlyft/no-scope"
)
