// Package update checks, at most once a day, whether a newer release of the
// CLI exists. It is the CLI's only network call that is not an API request:
// an unauthenticated GET of the latest GitHub release. Nothing about the
// user or their usage is sent. There is no telemetry in v1.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// LatestReleaseURL is the GitHub API endpoint for the latest release.
const LatestReleaseURL = "https://api.github.com/repos/devlyft/cli/releases/latest"

// Interval is the minimum time between checks.
const Interval = 24 * time.Hour

// DisableEnv disables the update notifier when set to any non-empty value.
const DisableEnv = "DEVLYFT_NO_UPDATE_NOTIFIER"

// Release is a newer release than the running one.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// state is persisted between runs.
type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    Release   `json:"latest"`
}

// Enabled reports whether the notifier may run: not in CI, not when the
// user turned it off, only when stderr is a terminal and only for release
// builds with a semver version.
func Enabled(getenv func(string) string, stderrTTY bool, current string) bool {
	switch {
	case getenv("CI") != "", getenv(DisableEnv) != "":
		return false
	case !stderrTTY:
		return false
	}
	return semver.IsValid(canonical(current))
}

// Checker looks up the latest release.
type Checker struct {
	// Current is the running version, with or without a leading "v".
	Current string
	// StatePath is where the time of the last check is recorded.
	StatePath string
	// URL is the latest-release endpoint; LatestReleaseURL by default.
	URL    string
	Client *http.Client
	Now    func() time.Time
}

// DefaultStatePath returns the state file location in the user cache
// directory.
func DefaultStatePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "devlyft", "update-state.json"), nil
}

// Check returns the latest release when it is newer than the running
// version. It returns nil when it is not, or when a check already ran
// within Interval, in which case it makes no request. Errors are for
// debug logging only; callers stay silent.
func (c *Checker) Check(ctx context.Context) (*Release, error) {
	now := c.now()
	if st, err := c.readState(); err == nil && now.Sub(st.CheckedAt) < Interval && !st.CheckedAt.After(now) {
		return nil, nil
	}

	latest, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.writeState(state{CheckedAt: now, Latest: *latest}); err != nil {
		return nil, err
	}
	if semver.Compare(canonical(latest.Version), canonical(c.Current)) <= 0 {
		return nil, nil
	}
	return latest, nil
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	url := c.URL
	if url == "" {
		url = LatestReleaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "devlyft-cli/"+strings.TrimPrefix(c.Current, "v"))

	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("latest release: %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("latest release: %w", err)
	}
	if !semver.IsValid(canonical(body.TagName)) {
		return nil, fmt.Errorf("latest release: invalid tag %q", body.TagName)
	}
	return &Release{Version: strings.TrimPrefix(body.TagName, "v"), URL: body.HTMLURL}, nil
}

func (c *Checker) readState() (state, error) {
	var st state
	b, err := os.ReadFile(c.StatePath)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

func (c *Checker) writeState(st state) error {
	if c.StatePath == "" {
		return errors.New("no state path")
	}
	dir := filepath.Dir(c.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// CreateTemp creates the file with mode 0600; renaming keeps it.
	tmp, err := os.CreateTemp(dir, ".update-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.StatePath)
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Notice formats the message printed to stderr. homebrew adds the upgrade
// command for Homebrew installs.
func Notice(current string, r *Release, homebrew bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nA new release of devlyft is available: %s → %s\n",
		strings.TrimPrefix(current, "v"), r.Version)
	if homebrew {
		b.WriteString("To upgrade, run: brew upgrade devlyft/tap/devlyft\n")
	}
	if r.URL != "" {
		b.WriteString(r.URL + "\n")
	}
	return b.String()
}

// IsHomebrew reports whether the executable at path was installed by
// Homebrew.
func IsHomebrew(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.ToSlash(path)
	for _, prefix := range []string{"/opt/homebrew/", "/home/linuxbrew/.linuxbrew/", "/usr/local/Cellar/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.Contains(path, "/Cellar/")
}

func canonical(v string) string {
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
