// Package redact removes tokens and secret values from text before it is
// logged. Debug output must pass through it.
package redact

import (
	"net/http"
	"regexp"
	"strings"
)

// Placeholder replaces every redacted value.
const Placeholder = "[REDACTED]"

// sensitiveHeaders are HTTP headers whose values are always redacted.
var sensitiveHeaders = map[string]bool{
	"Authorization":          true,
	"Proxy-Authorization":    true,
	"Cookie":                 true,
	"Set-Cookie":             true,
	"Sec-Websocket-Protocol": true,
	"X-Api-Key":              true,
}

var (
	// "Bearer <token>" anywhere, including the WebSocket "bearer, <token>"
	// subprotocol form.
	bearerRE = regexp.MustCompile(`(?i)(bearer,?\s+)[A-Za-z0-9\-._~+/]+=*`)
	// JSON string fields with a sensitive name.
	jsonFieldRE = regexp.MustCompile(`(?i)("(?:[a-z_]*token|secret|password|value|code_verifier|code|client_secret|private_key)"\s*:\s*)"(?:[^"\\]|\\.)*"`)
	// Query and form parameters with a sensitive name.
	paramRE = regexp.MustCompile(`(?i)\b((?:[a-z_]*token|secret|password|code_verifier|code|client_secret)=)[^&\s"]+`)
	// JWTs on their own.
	jwtRE = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	// DevLyft API tokens: "dvl_" and 32 random bytes in base64url.
	apiTokenRE = regexp.MustCompile(`\bdvl_[A-Za-z0-9_-]{16,}`)
)

// String redacts bearer tokens, JWTs, sensitive JSON fields and sensitive
// query or form parameters in s.
func String(s string) string {
	s = bearerRE.ReplaceAllString(s, "${1}"+Placeholder)
	s = jsonFieldRE.ReplaceAllString(s, `${1}"`+Placeholder+`"`)
	s = paramRE.ReplaceAllString(s, "${1}"+Placeholder)
	s = jwtRE.ReplaceAllString(s, Placeholder)
	s = apiTokenRE.ReplaceAllString(s, Placeholder)
	return s
}

// Header returns a copy of h with sensitive header values redacted.
func Header(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		if sensitiveHeaders[http.CanonicalHeaderKey(k)] {
			out[k] = []string{Placeholder}
			continue
		}
		cp := make([]string, len(vs))
		for i, v := range vs {
			cp[i] = String(v)
		}
		out[k] = cp
	}
	return out
}

// Secrets returns a function that replaces each of the given literal values
// with the placeholder. Use it for values the CLI knows are secret, such as
// a secret being set, that the patterns above cannot recognise.
func Secrets(values ...string) func(string) string {
	var pairs []string
	for _, v := range values {
		if v != "" {
			pairs = append(pairs, v, Placeholder)
		}
	}
	r := strings.NewReplacer(pairs...)
	return r.Replace
}
