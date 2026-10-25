// Package config reads and writes the config file, resolves the scope a
// command runs in (API URL, context, account, org, project, environment and
// location) and builds the HTTP client with proxy and CA bundle settings.
//
// Every scope value is resolved by the same precedence: flag, then
// environment variable, then the active context (or, for api_url and
// ca_bundle, the top level of the config file), then the default.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/devlyft/cli/internal/clierr"
)

// DefaultAPIURL is the production API.
const DefaultAPIURL = "https://api.devlyft.io"

// FileName is the config file inside Dir.
const FileName = "config.yaml"

// File is the config file. Keys this version does not know are kept in
// Extra and written back unchanged, so an older CLI never drops settings a
// newer one wrote.
type File struct {
	APIURL         string              `yaml:"api_url,omitempty"`
	CABundle       string              `yaml:"ca_bundle,omitempty"`
	CurrentContext string              `yaml:"current_context,omitempty"`
	Contexts       map[string]*Context `yaml:"contexts,omitempty"`
	Extra          map[string]any      `yaml:",inline"`

	path string
}

// Context is a named set of defaults for the scope a command runs in.
// Values are names or IDs, resolved when a command needs them.
type Context struct {
	Account  string         `yaml:"account,omitempty" json:"account,omitempty"`
	Org      string         `yaml:"org,omitempty" json:"org,omitempty"`
	Project  string         `yaml:"project,omitempty" json:"project,omitempty"`
	Env      string         `yaml:"env,omitempty" json:"env,omitempty"`
	Location string         `yaml:"location,omitempty" json:"location,omitempty"`
	Extra    map[string]any `yaml:",inline" json:"-"`
}

// Dir returns the CLI's config directory: ~/.config/devlyft on Linux,
// ~/Library/Application Support/devlyft on macOS and %AppData%\devlyft on
// Windows. XDG_CONFIG_HOME is honoured where os.UserConfigDir honours it.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "devlyft"), nil
}

// Load reads dir/config.yaml. A missing file is an empty config.
func Load(dir string) (*File, error) {
	path := filepath.Join(dir, FileName)
	f := &File{path: path}
	b, err := os.ReadFile(path) //nolint:gosec // the CLI's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return f, nil
	}
	if err := yaml.Unmarshal(b, f); err != nil {
		e := clierr.New(clierr.CodeInvalidArgument, "invalid config file %s: %v", path, err)
		e.Hint = "Fix or remove the file."
		return nil, e
	}
	for name, c := range f.Contexts {
		if c == nil {
			f.Contexts[name] = &Context{}
		}
	}
	return f, nil
}

// Path is the file the config was loaded from.
func (f *File) Path() string { return f.path }

// Save writes the config atomically with mode 0600 in a 0700 directory.
func (f *File) Save() error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	b := buf.Bytes()
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmp.Name(), f.path); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ContextNames returns the context names, sorted.
func (f *File) ContextNames() []string {
	names := make([]string, 0, len(f.Contexts))
	for n := range f.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Keys are the top-level settings `config get|set|list` manage.
var Keys = []string{"api_url", "ca_bundle", "current_context"}

// Get returns a top-level setting.
func (f *File) Get(key string) (string, error) {
	switch key {
	case "api_url":
		return f.APIURL, nil
	case "ca_bundle":
		return f.CABundle, nil
	case "current_context":
		return f.CurrentContext, nil
	}
	return "", unknownKey(key)
}

// Set validates and sets a top-level setting. An empty value unsets it.
func (f *File) Set(key, value string) error {
	switch key {
	case "api_url":
		if value != "" {
			u, err := NormalizeAPIURL(value)
			if err != nil {
				return err
			}
			value = u
		}
		f.APIURL = value
	case "ca_bundle":
		if value != "" {
			abs, err := filepath.Abs(value)
			if err != nil {
				return clierr.Usagef("invalid ca_bundle %q: %v", value, err)
			}
			if _, err := LoadCABundle(abs); err != nil {
				return err
			}
			value = abs
		}
		f.CABundle = value
	case "current_context":
		if value != "" {
			if _, ok := f.Contexts[value]; !ok {
				return NoSuchContext(value)
			}
		}
		f.CurrentContext = value
	default:
		return unknownKey(key)
	}
	return nil
}

func unknownKey(key string) *clierr.Error {
	return clierr.Usagef("unknown config key %q: must be one of %s", key, strings.Join(Keys, ", "))
}

// NoSuchContext is the error for a context name that does not exist.
func NoSuchContext(name string) *clierr.Error {
	e := clierr.New(clierr.CodeNotFound, "context %q does not exist", name)
	e.Hint = "Run `devlyft context list` to see contexts, or `devlyft context use " + name + "` to create it."
	return e
}

// NormalizeAPIURL validates an API URL and strips trailing slashes. It must
// use https, except for a loopback host.
func NormalizeAPIURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", clierr.Usagef("invalid API URL %q: must be an absolute https URL", s)
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "", clierr.Usagef("invalid API URL %q: must use https", s)
	}
	return strings.TrimRight(s, "/"), nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// Host returns the lower-case host[:port] of an API URL, the key that
// credentials are stored under. It returns the input unchanged if it does
// not parse as a URL with a host.
func Host(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return apiURL
	}
	return strings.ToLower(u.Host)
}
