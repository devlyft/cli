// Package credstore stores credentials in the system keyring, or in a 0600
// file when no keyring is usable.
//
// An index file, credentials.json in the config directory, records every
// stored credential with its metadata and which store holds its secret
// (credential_store: keyring|file). Reads go straight to that store, so
// commands never probe D-Bus for a credential that lives in the file.
// Findings behind the design: docs/spikes/2026-10-credential-storage.md.
package credstore

import (
	"context"
	"errors"
	"time"
)

//go:generate go tool mockgen -source=credstore.go -destination=mock/credstore.go -package=mock

// Kind names the store that holds a credential's secret.
type Kind string

// Stores.
const (
	KindKeyring Kind = "keyring"
	KindFile    Kind = "file"
)

// ErrNotFound means no credential is stored for the host and account.
var ErrNotFound = errors.New("credential not found")

// Organization is an organization the credential could see when it was
// stored.
type Organization struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
	Name string `json:"name"`
}

// Credential is a stored API credential.
type Credential struct {
	// Host is the API host[:port] the credential is for.
	Host string `json:"host"`
	// Account names the credential within the host.
	Account string `json:"account"`
	// Store is where the secret lives. Set by the store on write.
	Store Kind `json:"credential_store"`
	// Token is the secret. It is empty in List results.
	Token string `json:"token,omitempty"`
	// Organizations the token could see when it was stored.
	Organizations []Organization `json:"organizations,omitempty"`
	// CreatedAt is when the credential was stored.
	CreatedAt time.Time `json:"created_at"`
}

// Store reads and writes credentials.
type Store interface {
	// Get returns the credential with its token, or ErrNotFound.
	Get(ctx context.Context, host, account string) (*Credential, error)
	// Set stores c, replacing any credential for the same host and
	// account. insecure forces the file store (--insecure-storage). It
	// sets c.Store to the store used.
	Set(ctx context.Context, c *Credential, insecure bool) error
	// Delete removes the credential, or returns ErrNotFound.
	Delete(ctx context.Context, host, account string) error
	// List returns every stored credential without its token. It reads
	// only the index file and never touches the keyring.
	List(ctx context.Context) ([]Credential, error)
}
