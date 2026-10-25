package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/net/http/httpproxy"

	"github.com/devlyft/cli/internal/clierr"
)

// HTTPOptions configure the HTTP client every network call uses: the API
// facade, the log stream and anything after them.
type HTTPOptions struct {
	// CABundle is a PEM file of extra trusted CAs, added to the system
	// pool. Empty uses the system pool alone.
	CABundle string
	// Getenv reads HTTPS_PROXY, HTTP_PROXY and NO_PROXY (either case).
	Getenv func(string) string
}

// NewHTTPClient returns a client that honours the proxy environment
// variables and trusts the CA bundle on top of the system roots.
func NewHTTPClient(opts HTTPOptions) (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not an *http.Transport")
	}
	t := base.Clone()

	proxy := proxyConfig(opts.Getenv).ProxyFunc()
	t.Proxy = func(r *http.Request) (*url.URL, error) { return proxy(r.URL) }

	if opts.CABundle != "" {
		pool, err := LoadCABundle(opts.CABundle)
		if err != nil {
			return nil, err
		}
		t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: t}, nil
}

// proxyConfig reads the proxy variables through getenv, so that tests and
// callers control them. Unlike http.ProxyFromEnvironment it does not cache
// them for the life of the process.
func proxyConfig(getenv func(string) string) *httpproxy.Config {
	get := func(upper, lower string) string {
		if v := getenv(upper); v != "" {
			return v
		}
		return getenv(lower)
	}
	return &httpproxy.Config{
		HTTPProxy:  get("HTTP_PROXY", "http_proxy"),
		HTTPSProxy: get("HTTPS_PROXY", "https_proxy"),
		NoProxy:    get("NO_PROXY", "no_proxy"),
		CGI:        getenv("REQUEST_METHOD") != "",
	}
}

// LoadCABundle returns the system roots plus the certificates in the PEM
// file at path.
func LoadCABundle(path string) (*x509.CertPool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a path the user configured
	if err != nil {
		return nil, clierr.Usagef("cannot read CA bundle: %v", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, clierr.Usagef("CA bundle %s contains no PEM certificates", path)
	}
	return pool, nil
}
