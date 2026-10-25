package credstore

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory keyring that counts calls and can fail or
// hang on demand.
type fakeKeyring struct {
	mu      sync.Mutex
	secrets map[string]string
	calls   int
	err     error         // returned by every call when set
	delay   time.Duration // every call sleeps first
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{secrets: map[string]string{}} }

func (k *fakeKeyring) begin() error {
	k.mu.Lock()
	k.calls++
	delay, err := k.delay, k.err
	k.mu.Unlock()
	time.Sleep(delay)
	return err
}

func (k *fakeKeyring) Get(service, user string) (string, error) {
	if err := k.begin(); err != nil {
		return "", err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	s, ok := k.secrets[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return s, nil
}

func (k *fakeKeyring) Set(service, user, secret string) error {
	if err := k.begin(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.secrets[service+"/"+user] = secret
	return nil
}

func (k *fakeKeyring) Delete(service, user string) error {
	if err := k.begin(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.secrets[service+"/"+user]; !ok {
		return keyring.ErrNotFound
	}
	delete(k.secrets, service+"/"+user)
	return nil
}

func (k *fakeKeyring) callCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls
}

// desktopEnv is a Linux desktop with a session bus.
func desktopEnv() Env {
	return testEnv("linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:abstract=/tmp/dbus-x"})
}

func testEnv(goos string, vars map[string]string, files ...string) Env {
	return Env{
		Getenv:     func(k string) string { return vars[k] },
		FileExists: func(p string) bool { return contains(files, p) },
		GOOS:       goos,
		UID:        1000,
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type harness struct {
	store    Store
	keyring  *fakeKeyring
	warnings []string
	dir      string
}

func newHarness(t *testing.T, env Env) *harness {
	t.Helper()
	h := &harness{keyring: newFakeKeyring(), dir: filepath.Join(t.TempDir(), "devlyft")}
	h.store = New(Options{
		Dir:     h.dir,
		Keyring: h.keyring,
		Env:     &env,
		Timeout: 50 * time.Millisecond,
		Warn:    func(msg string) { h.warnings = append(h.warnings, msg) },
	})
	return h
}

func cred(token string) *Credential {
	return &Credential{
		Host:          "api.devlyft.io",
		Account:       "default",
		Token:         token,
		Organizations: []Organization{{ID: "0b1c", Slug: "acme", Name: "Acme"}},
		CreatedAt:     time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	}
}

func (h *harness) indexFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKeyringRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())

	if err := h.store.Set(t.Context(), cred("dvl_secret"), false); err != nil {
		t.Fatal(err)
	}
	if len(h.warnings) != 0 {
		t.Errorf("unexpected warnings: %q", h.warnings)
	}
	if idx := h.indexFile(t); strings.Contains(idx, "dvl_secret") || !strings.Contains(idx, `"credential_store": "keyring"`) {
		t.Errorf("index should record the keyring and hold no token:\n%s", idx)
	}

	got, err := h.store.Get(t.Context(), "api.devlyft.io", "default")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "dvl_secret" || got.Store != KindKeyring || got.Organizations[0].Slug != "acme" {
		t.Errorf("Get() = %+v", got)
	}
}

// Each environment where the keyring can't help is skipped without a
// single keyring call (for D-Bus: no autolaunch), with one warning.
func TestKeyringSkipped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		env    Env
		reason string
	}{
		{"CI", testEnv("linux", map[string]string{"CI": "true", "DBUS_SESSION_BUS_ADDRESS": "unix:abstract=x"}), ReasonCI},
		{"container variable", testEnv("linux", map[string]string{"container": "podman", "DBUS_SESSION_BUS_ADDRESS": "unix:abstract=x"}), ReasonContainer},
		{"dockerenv", testEnv("darwin", nil, "/.dockerenv"), ReasonContainer},
		{"containerenv", testEnv("windows", nil, "/run/.containerenv"), ReasonContainer},
		{"no session bus", testEnv("linux", nil), ReasonNoDBus},
		{"bus address points nowhere", testEnv("linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus,guid=x"}), ReasonNoDBus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tt.env)

			c := cred("dvl_secret")
			if err := h.store.Set(t.Context(), c, false); err != nil {
				t.Fatal(err)
			}
			if n := h.keyring.callCount(); n != 0 {
				t.Errorf("keyring called %d times, want 0", n)
			}
			if c.Store != KindFile {
				t.Errorf("Store = %q, want file", c.Store)
			}
			if len(h.warnings) != 1 || !strings.Contains(h.warnings[0], "("+tt.reason+")") {
				t.Errorf("warnings = %q, want one naming %q", h.warnings, tt.reason)
			}
			got, err := h.store.Get(t.Context(), c.Host, c.Account)
			if err != nil || got.Token != "dvl_secret" {
				t.Errorf("Get() = %+v, %v", got, err)
			}
			if n := h.keyring.callCount(); n != 0 {
				t.Errorf("Get called the keyring %d times for a file credential", n)
			}
		})
	}
}

func TestSessionBusDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   Env
		found bool
	}{
		{"abstract address", testEnv("linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:abstract=/tmp/x"}), true},
		{"path address exists", testEnv("linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, "/run/user/1000/bus"), true},
		{"XDG_RUNTIME_DIR bus", testEnv("linux", map[string]string{"XDG_RUNTIME_DIR": "/run/user/42"}, filepath.Join(string(filepath.Separator)+"run", "user", "42", "bus")), true},
		{"default per-user bus", testEnv("linux", nil, "/run/user/1000/bus"), true},
		{"nothing", testEnv("linux", map[string]string{"XDG_RUNTIME_DIR": "/run/user/42"}), false},
	}
	for _, tt := range tests {
		if got := tt.env.hasSessionBus(); got != tt.found {
			t.Errorf("%s: hasSessionBus() = %v, want %v", tt.name, got, tt.found)
		}
	}
}

func TestInsecureStorage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())

	c := cred("dvl_secret")
	if err := h.store.Set(t.Context(), c, true); err != nil {
		t.Fatal(err)
	}
	if h.keyring.callCount() != 0 || c.Store != KindFile || len(h.warnings) != 0 {
		t.Errorf("calls=%d store=%q warnings=%q; want 0, file, none", h.keyring.callCount(), c.Store, h.warnings)
	}
}

// Any keyring error other than not-found falls back to the file with one
// warning naming the reason.
func TestKeyringErrorFallsBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		delay  time.Duration
		reason string
	}{
		{"no secret service", errors.New("The name org.freedesktop.secrets was not provided by any .service files"), 0, ReasonNoSecretService},
		{"locked", errors.New("Cannot create an item in a locked collection"), 0, ReasonLocked},
		{"dbus-launch missing", &exec.Error{Name: "dbus-launch", Err: exec.ErrNotFound}, 0, ReasonNoDBus},
		{"dbus", errors.New("dbus: authentication failed"), 0, ReasonNoDBus},
		{"too big", keyring.ErrSetDataTooBig, 0, "credential too large for the keyring"},
		{"other", errors.New("boom"), 0, "keyring error"},
		{"timeout", nil, time.Second, ReasonTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, desktopEnv())
			h.keyring.err, h.keyring.delay = tt.err, tt.delay

			c := cred("dvl_secret")
			start := time.Now()
			if err := h.store.Set(t.Context(), c, false); err != nil {
				t.Fatal(err)
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Errorf("Set took %v; the keyring timeout was not applied", time.Since(start))
			}
			if c.Store != KindFile {
				t.Errorf("Store = %q, want file", c.Store)
			}
			if len(h.warnings) != 1 || !strings.Contains(h.warnings[0], "("+tt.reason+")") {
				t.Errorf("warnings = %q, want one naming %q", h.warnings, tt.reason)
			}
			if !strings.Contains(h.indexFile(t), "dvl_secret") {
				t.Error("token was not written to the file")
			}
		})
	}
}

func TestGetKeyringErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())
	ctx := t.Context()
	if err := h.store.Set(ctx, cred("dvl_secret"), false); err != nil {
		t.Fatal(err)
	}

	// The keyring entry vanished: the index entry is stale.
	h.keyring.secrets = map[string]string{}
	if _, err := h.store.Get(ctx, "api.devlyft.io", "default"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}

	h.keyring.err = errors.New("Cannot unlock: locked")
	_, err := h.store.Get(ctx, "api.devlyft.io", "default")
	uerr, ok := errors.AsType[*UnavailableError](err)
	if !ok || uerr.Reason != ReasonLocked {
		t.Errorf("Get() error = %v, want UnavailableError(%s)", err, ReasonLocked)
	}

	if _, err := h.store.Get(ctx, "other.example.com", "default"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown host) error = %v, want ErrNotFound", err)
	}
}

// Replacing a keyring credential with a file one removes the old secret
// from the keyring.
func TestReplaceRemovesStaleKeyringSecret(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())
	ctx := t.Context()

	if err := h.store.Set(ctx, cred("dvl_old"), false); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Set(ctx, cred("dvl_new"), true); err != nil {
		t.Fatal(err)
	}
	if len(h.keyring.secrets) != 0 {
		t.Errorf("stale keyring secrets: %v", h.keyring.secrets)
	}
	got, err := h.store.Get(ctx, "api.devlyft.io", "default")
	if err != nil || got.Token != "dvl_new" || got.Store != KindFile {
		t.Errorf("Get() = %+v, %v", got, err)
	}
	if list, _ := h.store.List(ctx); len(list) != 1 {
		t.Errorf("List() has %d entries, want 1", len(list))
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("keyring", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, desktopEnv())
		if err := h.store.Set(ctx, cred("dvl_secret"), false); err != nil {
			t.Fatal(err)
		}
		if err := h.store.Delete(ctx, "api.devlyft.io", "default"); err != nil {
			t.Fatal(err)
		}
		if len(h.keyring.secrets) != 0 {
			t.Error("secret left in keyring")
		}
		if err := h.store.Delete(ctx, "api.devlyft.io", "default"); !errors.Is(err, ErrNotFound) {
			t.Errorf("second Delete() = %v, want ErrNotFound", err)
		}
	})

	t.Run("keyring unavailable still forgets the credential", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, desktopEnv())
		if err := h.store.Set(ctx, cred("dvl_secret"), false); err != nil {
			t.Fatal(err)
		}
		h.keyring.err = errors.New("dbus: connection closed")
		if err := h.store.Delete(ctx, "api.devlyft.io", "default"); err != nil {
			t.Fatal(err)
		}
		if len(h.warnings) != 1 || !strings.Contains(h.warnings[0], "devlyft:api.devlyft.io") {
			t.Errorf("warnings = %q", h.warnings)
		}
		if list, _ := h.store.List(ctx); len(list) != 0 {
			t.Errorf("List() = %v, want empty", list)
		}
	})
}

func TestListNeverTouchesKeyring(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())
	ctx := t.Context()

	if list, err := h.store.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("List() on a fresh store = %v, %v", list, err)
	}
	if err := h.store.Set(ctx, cred("dvl_a"), false); err != nil {
		t.Fatal(err)
	}
	c := cred("dvl_b")
	c.Host = "api.staging.example.com"
	if err := h.store.Set(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	before := h.keyring.callCount()

	list, err := h.store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.keyring.callCount() != before {
		t.Error("List() called the keyring")
	}
	if len(list) != 2 || list[0].Store != KindKeyring || list[1].Store != KindFile {
		t.Errorf("List() = %+v", list)
	}
	for _, c := range list {
		if c.Token != "" {
			t.Errorf("List() leaked the token for %s", c.Host)
		}
	}
}

func TestFileModes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions only")
	}
	h := newHarness(t, testEnv(runtime.GOOS, map[string]string{"CI": "1"}))
	if err := h.store.Set(t.Context(), cred("dvl_secret"), false); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{h.dir: 0o700, filepath.Join(h.dir, FileName): 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	entries, _ := os.ReadDir(h.dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestBadFiles(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"corrupt": "{not json",
		"newer":   `{"version": 99, "credentials": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, desktopEnv())
			if err := os.MkdirAll(h.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.dir, FileName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := h.store.List(t.Context()); err == nil {
				t.Error("List() succeeded on a bad file")
			}
			if err := h.store.Set(t.Context(), cred("dvl_x"), true); err == nil {
				t.Error("Set() overwrote a bad file")
			}
		})
	}
}

func TestSetValidates(t *testing.T) {
	t.Parallel()
	h := newHarness(t, desktopEnv())
	if err := h.store.Set(t.Context(), &Credential{Host: "h", Account: "a"}, false); err == nil {
		t.Error("Set() accepted an empty token")
	}
}

func TestWarningText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      Env
		contains []string
		excludes []string
	}{
		{"CI", testEnv("linux", map[string]string{"CI": "1"}), []string{"(running in CI)", "(mode 0600)", "Use DEVLYFT_API_TOKEN in CI"}, []string{"gnome-keyring"}},
		{"headless Linux", testEnv("linux", nil), []string{"(no D-Bus session)", "gnome-keyring"}, []string{"DEVLYFT_API_TOKEN"}},
		{"Windows container", testEnv("windows", nil, "/.dockerenv"), []string{"(running in a container)"}, []string{"mode 0600", "gnome-keyring"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tt.env)
			if err := h.store.Set(t.Context(), cred("dvl_secret"), false); err != nil {
				t.Fatal(err)
			}
			w := strings.Join(h.warnings, "\n")
			if !strings.HasPrefix(w, "Warning: no system keyring available") || !strings.Contains(w, FileName) {
				t.Errorf("warning = %q", w)
			}
			for _, s := range tt.contains {
				if !strings.Contains(w, s) {
					t.Errorf("warning %q missing %q", w, s)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(w, s) {
					t.Errorf("warning %q should not contain %q", w, s)
				}
			}
		})
	}
}

func TestIndexShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testEnv("linux", map[string]string{"CI": "1"}))
	if err := h.store.Set(t.Context(), cred("dvl_secret"), false); err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version     int              `json:"version"`
		Credentials []map[string]any `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(h.indexFile(t)), &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Credentials) != 1 || f.Credentials[0]["credential_store"] != "file" {
		t.Errorf("index = %+v", f)
	}
}

func TestUnavailableError(t *testing.T) {
	t.Parallel()
	e := &UnavailableError{Reason: ReasonLocked}
	if !strings.Contains(e.Error(), ReasonLocked) {
		t.Errorf("Error() = %q", e.Error())
	}
	inner := errors.New("inner")
	e = &UnavailableError{Reason: ReasonLocked, Err: inner}
	if !errors.Is(e, inner) || !strings.Contains(e.Error(), "inner") {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestSystemDefaults(t *testing.T) {
	t.Parallel()
	env := SystemEnv()
	if env.GOOS != runtime.GOOS || env.Getenv == nil || env.FileExists == nil {
		t.Errorf("SystemEnv() = %+v", env)
	}
	if !env.FileExists(".") || env.FileExists(filepath.Join(t.TempDir(), "missing")) {
		t.Error("FileExists is wrong")
	}
	// New fills defaults without touching the keyring.
	if New(Options{Dir: t.TempDir()}) == nil {
		t.Error("New() returned nil")
	}
	if SystemKeyring() == nil {
		t.Error("SystemKeyring() returned nil")
	}
}
