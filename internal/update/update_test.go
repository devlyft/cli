package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		tty     bool
		version string
		want    bool
	}{
		{name: "release build at a terminal", tty: true, version: "0.1.0", want: true},
		{name: "v-prefixed version", tty: true, version: "v0.1.0", want: true},
		{name: "CI set", env: map[string]string{"CI": "true"}, tty: true, version: "0.1.0"},
		{name: "CI set to anything", env: map[string]string{"CI": "1"}, tty: true, version: "0.1.0"},
		{name: "disabled by env", env: map[string]string{DisableEnv: "1"}, tty: true, version: "0.1.0"},
		{name: "stderr not a terminal", tty: false, version: "0.1.0"},
		{name: "dev build", tty: true, version: "dev"},
		{name: "empty version", tty: true, version: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Enabled(env(tt.env), tt.tty, tt.version); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// releaseServer serves tag as the latest release and counts requests.
func releaseServer(t *testing.T, status int, tag string) (url string, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("update check must be unauthenticated")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": tag,
			"html_url": "https://github.com/devlyft/cli/releases/tag/" + tag,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, hits
}

var now = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func newChecker(t *testing.T, url, current string) *Checker {
	t.Helper()
	return &Checker{
		Current:   current,
		StatePath: filepath.Join(t.TempDir(), "devlyft", "update-state.json"),
		URL:       url,
		Now:       func() time.Time { return now },
	}
}

func TestCheckVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		tag     string
		newer   bool
	}{
		{name: "newer minor", current: "0.1.0", tag: "v0.2.0", newer: true},
		{name: "newer patch", current: "v0.1.0", tag: "v0.1.1", newer: true},
		{name: "equal", current: "0.2.0", tag: "v0.2.0"},
		{name: "older", current: "0.3.0", tag: "v0.2.0"},
		{name: "local prerelease of the latest", current: "0.2.0-rc.1", tag: "v0.2.0", newer: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url, _ := releaseServer(t, http.StatusOK, tt.tag)
			c := newChecker(t, url, tt.current)

			rel, err := c.Check(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if (rel != nil) != tt.newer {
				t.Fatalf("Check() = %+v, want newer=%v", rel, tt.newer)
			}
			if rel != nil && rel.Version != strings.TrimPrefix(tt.tag, "v") {
				t.Errorf("Version = %q", rel.Version)
			}
		})
	}
}

func TestCheckAtMostOnceADay(t *testing.T) {
	t.Parallel()

	url, hits := releaseServer(t, http.StatusOK, "v0.2.0")
	c := newChecker(t, url, "0.1.0")

	if rel, err := c.Check(t.Context()); err != nil || rel == nil {
		t.Fatalf("first check = %+v, %v; want a release", rel, err)
	}

	// Within 24 hours: no request and no notice.
	c.Now = func() time.Time { return now.Add(23 * time.Hour) }
	if rel, err := c.Check(t.Context()); err != nil || rel != nil {
		t.Fatalf("second check = %+v, %v; want nothing", rel, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", hits.Load())
	}

	// After 24 hours: checks again.
	c.Now = func() time.Time { return now.Add(25 * time.Hour) }
	if rel, err := c.Check(t.Context()); err != nil || rel == nil {
		t.Fatalf("third check = %+v, %v; want a release", rel, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("requests = %d, want 2", hits.Load())
	}
}

func TestCheckClockMovedBackwards(t *testing.T) {
	t.Parallel()

	url, hits := releaseServer(t, http.StatusOK, "v0.2.0")
	c := newChecker(t, url, "0.1.0")
	if _, err := c.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.Now = func() time.Time { return now.Add(-48 * time.Hour) }
	if _, err := c.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("requests = %d, want 2: a check time in the future must not suppress checks forever", hits.Load())
	}
}

func TestCheckStateFile(t *testing.T) {
	t.Parallel()

	url, _ := releaseServer(t, http.StatusOK, "v0.2.0")
	c := newChecker(t, url, "0.1.0")
	if _, err := c.Check(t.Context()); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(c.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
	b, err := os.ReadFile(c.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if !st.CheckedAt.Equal(now) || st.Latest.Version != "0.2.0" {
		t.Errorf("state = %+v", st)
	}
}

func TestCheckFailuresAreSilentAndRetried(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		tag    string
	}{
		{name: "server error", status: http.StatusInternalServerError, tag: "v0.2.0"},
		{name: "rate limited", status: http.StatusForbidden, tag: "v0.2.0"},
		{name: "invalid tag", status: http.StatusOK, tag: "latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url, hits := releaseServer(t, tt.status, tt.tag)
			c := newChecker(t, url, "0.1.0")

			for range 2 {
				if rel, err := c.Check(t.Context()); err == nil || rel != nil {
					t.Fatalf("Check() = %+v, %v; want an error and no release", rel, err)
				}
			}
			// A failed check is not recorded, so the next run tries again.
			if hits.Load() != 2 {
				t.Errorf("requests = %d, want 2", hits.Load())
			}
		})
	}
}

func TestCheckNetworkFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	c := newChecker(t, url, "0.1.0")
	if rel, err := c.Check(t.Context()); err == nil || rel != nil {
		t.Fatalf("Check() = %+v, %v; want an error and no release", rel, err)
	}
}

func TestNotice(t *testing.T) {
	t.Parallel()

	rel := &Release{Version: "0.2.0", URL: "https://github.com/devlyft/cli/releases/tag/v0.2.0"}

	got := Notice("v0.1.0", rel, false)
	want := "\nA new release of devlyft is available: 0.1.0 → 0.2.0\nhttps://github.com/devlyft/cli/releases/tag/v0.2.0\n"
	if got != want {
		t.Errorf("Notice() = %q, want %q", got, want)
	}
	if got := Notice("0.1.0", rel, true); !strings.Contains(got, "brew upgrade devlyft/tap/devlyft") {
		t.Errorf("Homebrew notice missing upgrade command: %q", got)
	}
}

func TestIsHomebrew(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"/opt/homebrew/bin/devlyft":                            true,
		"/usr/local/Cellar/devlyft/0.1.0/bin/devlyft":          true,
		"/home/linuxbrew/.linuxbrew/bin/devlyft":               true,
		"/usr/local/bin/devlyft":                               false,
		"/home/me/go/bin/devlyft":                              false,
		"/home/linuxbrew/.linuxbrew/Cellar/devlyft/0.1.0/bin/": true,
	} {
		if got := IsHomebrew(path); got != want {
			t.Errorf("IsHomebrew(%q) = %v, want %v", path, got, want)
		}
	}
}
