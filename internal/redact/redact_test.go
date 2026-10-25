package redact

import (
	"net/http"
	"strings"
	"testing"
)

const jwt = "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ1In0.c2lnbmF0dXJl"

func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"bearer header value", "Authorization: Bearer abc.def-123", "Authorization: Bearer [REDACTED]"},
		{"websocket subprotocol", "Sec-WebSocket-Protocol: bearer, abc123", "Sec-WebSocket-Protocol: bearer, [REDACTED]"},
		{"bare jwt", "token is " + jwt + " ok", "token is [REDACTED] ok"},
		{"bare api token", "read dvl_AbC-123_xyzXYZ0987654321 from stdin", "read [REDACTED] from stdin"},
		{"short dvl_ word is not a token", "dvl_x", "dvl_x"},
		{"json access token", `{"access_token":"abc","expires_in":900}`, `{"access_token":"[REDACTED]","expires_in":900}`},
		{"json refresh token with spaces", `{"refresh_token" : "a\"b"}`, `{"refresh_token" : "[REDACTED]"}`},
		{"json secret value", `{"key":"DB_URL","value":"postgres://u:p@h/db"}`, `{"key":"DB_URL","value":"[REDACTED]"}`},
		{"form body", "grant_type=refresh_token&refresh_token=xyz&client_id=devlyft-cli", "grant_type=refresh_token&refresh_token=[REDACTED]&client_id=devlyft-cli"},
		{"query code and verifier", "/token?code=abc&code_verifier=def&state=s", "/token?code=[REDACTED]&code_verifier=[REDACTED]&state=s"},
		{"nothing sensitive", `GET /v1/deploys?name=web 200`, `GET /v1/deploys?name=web 200`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := String(tt.in); got != tt.want {
				t.Errorf("String(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token")
	h.Set("Sec-WebSocket-Protocol", "bearer, secret-token")
	h.Set("Cookie", "session=secret-token")
	h.Set("X-Request-Id", "req-1")
	h.Set("Location", "https://example.com/cb?code=secret-token")

	got := Header(h)
	for k, vs := range got {
		for _, v := range vs {
			if strings.Contains(v, "secret-token") {
				t.Errorf("header %s leaked secret: %q", k, v)
			}
		}
	}
	if got.Get("X-Request-Id") != "req-1" {
		t.Errorf("X-Request-Id = %q, want unchanged", got.Get("X-Request-Id"))
	}
	if h.Get("Authorization") != "Bearer secret-token" {
		t.Error("Header modified its input")
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()

	r := Secrets("hunter2", "")
	if got := r("password is hunter2"); got != "password is [REDACTED]" {
		t.Errorf("got %q", got)
	}
}
