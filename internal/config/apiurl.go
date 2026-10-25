// Package config reads the config file and contexts and resolves org,
// project, environment and location.
//
// Until contexts land (devlyft/devlyft#244) it only resolves the API URL and
// the config directory.
package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAPIURL is the production API.
const DefaultAPIURL = "https://api.devlyft.io"

// ResolveAPIURL returns the API URL by precedence: the --api-url flag, then
// DEVLYFT_API_URL, then the default.
func ResolveAPIURL(flag string, getenv func(string) string) string {
	if flag != "" {
		return strings.TrimRight(flag, "/")
	}
	if v := getenv("DEVLYFT_API_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultAPIURL
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
