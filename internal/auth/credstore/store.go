package credstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// FileName is the index and fallback store in the config directory.
const FileName = "credentials.json"

const fileVersion = 1

// Options configure a Store.
type Options struct {
	// Dir is the config directory holding credentials.json.
	Dir string
	// Keyring defaults to SystemKeyring().
	Keyring Keyring
	// Env defaults to SystemEnv().
	Env *Env
	// Timeout bounds each keyring call; see TimeoutInteractive.
	Timeout time.Duration
	// Warn receives the one-time warning when a write falls back to the
	// file. nil discards it.
	Warn func(msg string)
}

// New returns a Store backed by the keyring and credentials.json in
// opts.Dir. Creating it touches neither.
func New(opts Options) Store {
	if opts.Keyring == nil {
		opts.Keyring = SystemKeyring()
	}
	env := SystemEnv()
	if opts.Env != nil {
		env = *opts.Env
	}
	if opts.Timeout <= 0 {
		opts.Timeout = TimeoutNonInteractive
	}
	if opts.Warn == nil {
		opts.Warn = func(string) {}
	}
	return &store{
		path:    filepath.Join(opts.Dir, FileName),
		keyring: boundedKeyring{k: opts.Keyring, timeout: opts.Timeout},
		env:     env,
		warn:    opts.Warn,
	}
}

type store struct {
	path    string
	keyring boundedKeyring
	env     Env
	warn    func(string)
}

var _ Store = (*store)(nil)

// file is the on-disk shape of credentials.json.
type file struct {
	Version     int          `json:"version"`
	Credentials []Credential `json:"credentials"`
}

// service is the keyring service name for a host.
func service(host string) string { return "devlyft:" + host }

func (s *store) Get(_ context.Context, host, account string) (*Credential, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	i := find(f, host, account)
	if i < 0 {
		return nil, ErrNotFound
	}
	c := f.Credentials[i]
	if c.Store == KindKeyring {
		token, err := s.keyring.get(service(host), account)
		if err != nil {
			return nil, err
		}
		c.Token = token
	}
	if c.Token == "" {
		return nil, ErrNotFound
	}
	return &c, nil
}

func (s *store) Set(_ context.Context, c *Credential, insecure bool) error {
	if c.Host == "" || c.Account == "" || c.Token == "" {
		return errors.New("credential needs a host, account and token")
	}
	f, err := s.read()
	if err != nil {
		return err
	}

	c.Store = KindFile
	switch reason, skip := s.env.skipKeyring(); {
	case insecure:
		// Chosen deliberately: no warning.
	case skip:
		s.warnFallback(reason)
	default:
		err := s.keyring.set(service(c.Host), c.Account, c.Token)
		if uerr, ok := errors.AsType[*UnavailableError](err); ok {
			s.warnFallback(uerr.Reason)
		} else if err != nil {
			return err
		} else {
			c.Store = KindKeyring
		}
	}

	entry := *c
	if entry.Store == KindKeyring {
		entry.Token = ""
	}
	if i := find(f, c.Host, c.Account); i >= 0 {
		if f.Credentials[i].Store == KindKeyring && c.Store == KindFile {
			// Don't leave the old secret behind in the keyring.
			_ = s.keyring.delete(service(c.Host), c.Account)
		}
		f.Credentials[i] = entry
	} else {
		f.Credentials = append(f.Credentials, entry)
	}
	return s.write(f)
}

func (s *store) Delete(_ context.Context, host, account string) error {
	f, err := s.read()
	if err != nil {
		return err
	}
	i := find(f, host, account)
	if i < 0 {
		return ErrNotFound
	}
	if f.Credentials[i].Store == KindKeyring {
		err := s.keyring.delete(service(host), account)
		if uerr, ok := errors.AsType[*UnavailableError](err); ok {
			s.warn(fmt.Sprintf("Warning: could not remove the token from the system keyring (%s); "+
				"remove the %q entry from it yourself.", uerr.Reason, service(host)))
		}
	}
	f.Credentials = slices.Delete(f.Credentials, i, i+1)
	return s.write(f)
}

func (s *store) List(context.Context) ([]Credential, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Credential, len(f.Credentials))
	for i, c := range f.Credentials {
		c.Token = ""
		out[i] = c
	}
	return out, nil
}

func (s *store) warnFallback(reason string) {
	mode := " (mode 0600)"
	if s.env.GOOS == "windows" {
		mode = ""
	}
	msg := fmt.Sprintf("Warning: no system keyring available (%s); credentials stored in %s%s.", reason, s.path, mode)
	switch reason {
	case ReasonCI, ReasonContainer:
		msg += "\nUse DEVLYFT_API_TOKEN in CI instead of `devlyft auth login --with-token`."
	case ReasonNoDBus, ReasonNoSecretService, ReasonLocked:
		if s.env.GOOS == "linux" {
			msg += "\nInstall and unlock gnome-keyring (or another Secret Service provider) to use the system keyring."
		}
	}
	s.warn(msg)
}

func find(f *file, host, account string) int {
	return slices.IndexFunc(f.Credentials, func(c Credential) bool {
		return c.Host == host && c.Account == account
	})
}

func (s *store) read() (*file, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &file{Version: fileVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}
	if f.Version > fileVersion {
		return nil, fmt.Errorf("%s was written by a newer devlyft (version %d)", s.path, f.Version)
	}
	return &f, nil
}

// write replaces credentials.json atomically: the file is written in full
// to a temporary file with mode 0600, then renamed over the old one.
func (s *store) write(f *file) error {
	f.Version = fileVersion
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if s.env.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs the execute bit
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil && s.env.GOOS != "windows" {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
