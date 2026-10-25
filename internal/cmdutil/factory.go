// Package cmdutil holds what every command needs: streams, the result
// printer and the debug logger. Command packages receive a *Factory when
// they are constructed; its fields are ready by the time RunE runs.
package cmdutil

import (
	"log/slog"

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

	// APIURL is the API origin from --api-url, DEVLYFT_API_URL or the
	// default.
	APIURL string
	// Getenv reads the environment.
	Getenv func(string) string
	// Credentials opens the credential store.
	Credentials func() (credstore.Store, error)
	// NewAPIClient returns an API client for APIURL using tokens.
	NewAPIClient func(tokens api.TokenSource) (api.Client, error)
}

// Host is the API host credentials are stored under.
func (f *Factory) Host() string { return config.Host(f.APIURL) }

// Resolver finds the credential for a command.
func (f *Factory) Resolver() *auth.Resolver {
	return &auth.Resolver{Getenv: f.Getenv, Store: f.Credentials}
}

// APIClient returns an API client authenticated with the resolved
// credential. The credential is resolved on the first request.
func (f *Factory) APIClient() (api.Client, error) {
	return f.NewAPIClient(&auth.TokenSource{Resolver: f.Resolver(), Host: f.Host()})
}

// YesFlagUsage is the help text for the --yes flag on destructive commands.
const YesFlagUsage = "Skip the confirmation prompt (required when not running interactively)"
