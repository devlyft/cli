package config

import "testing"

func TestResolveAPIURL(t *testing.T) {
	t.Parallel()

	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "DEVLYFT_API_URL" {
				return v
			}
			return ""
		}
	}
	tests := []struct {
		name, flag, env, want string
	}{
		{"default", "", "", DefaultAPIURL},
		{"env", "", "https://api.example.com/", "https://api.example.com"},
		{"flag beats env", "https://flag.example.com", "https://api.example.com", "https://flag.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveAPIURL(tt.flag, env(tt.env)); got != tt.want {
				t.Errorf("ResolveAPIURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHost(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://API.devlyft.io":    "api.devlyft.io",
		"https://api.devlyft.io/v1": "api.devlyft.io",
		"http://127.0.0.1:8080":     "127.0.0.1:8080",
		"not a url":                 "not a url",
	}
	for in, want := range tests {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDir(t *testing.T) {
	t.Parallel()

	dir, err := Dir()
	if err != nil {
		t.Skipf("no user config dir: %v", err)
	}
	if dir == "" {
		t.Error("Dir() returned empty path")
	}
}
