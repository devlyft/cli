package config

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// testCAPEM returns the certificate of a throwaway TLS server in PEM form.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

func TestCABundle(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	get := func(c *http.Client) error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	plain, err := NewHTTPClient(HTTPOptions{Getenv: env(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(plain); err == nil {
		t.Fatal("request to a server with an untrusted certificate succeeded without a CA bundle")
	}

	bundle := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(bundle, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	trusting, err := NewHTTPClient(HTTPOptions{CABundle: bundle, Getenv: env(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(trusting); err != nil {
		t.Fatalf("request with the CA bundle failed: %v", err)
	}

	if _, err := NewHTTPClient(HTTPOptions{CABundle: filepath.Join(t.TempDir(), "missing.pem"), Getenv: env(nil)}); err == nil {
		t.Error("missing CA bundle accepted")
	}
}

func TestProxyFromEnvironment(t *testing.T) {
	t.Parallel()

	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy receives the absolute URL.
		proxied = append(proxied, r.URL.String())
		_, _ = io.WriteString(w, "via proxy")
	}))
	t.Cleanup(proxy.Close)

	do := func(c *http.Client, target string) (string, error) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		if err != nil {
			return "", err
		}
		resp, err := c.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}

	c, err := NewHTTPClient(HTTPOptions{Getenv: env(map[string]string{"http_proxy": proxy.URL})})
	if err != nil {
		t.Fatal(err)
	}
	body, err := do(c, "http://api.devlyft.test/v1/organizations")
	if err != nil {
		t.Fatal(err)
	}
	if body != "via proxy" || len(proxied) != 1 || proxied[0] != "http://api.devlyft.test/v1/organizations" {
		t.Errorf("body %q, proxied %v", body, proxied)
	}

	// NO_PROXY bypasses the proxy; the direct request then fails to resolve.
	c, err = NewHTTPClient(HTTPOptions{Getenv: env(map[string]string{"HTTP_PROXY": proxy.URL, "NO_PROXY": ".devlyft.test"})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := do(c, "http://api.devlyft.test/"); err == nil {
		t.Error("NO_PROXY host went through the proxy")
	}
	if len(proxied) != 1 {
		t.Errorf("proxied %v", proxied)
	}
}
