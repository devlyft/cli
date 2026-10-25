package credstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Reasons the keyring is skipped or fails, shown in the fallback warning.
const (
	ReasonNoDBus          = "no D-Bus session"
	ReasonNoSecretService = "no Secret Service provider"
	ReasonLocked          = "keyring is locked"
	ReasonTimeout         = "keyring timed out"
	ReasonCI              = "running in CI"
	ReasonContainer       = "running in a container"
)

// Env is what keyring detection reads from the environment. Tests replace
// it so no detection touches the real system.
type Env struct {
	Getenv     func(string) string
	FileExists func(string) bool
	GOOS       string
	UID        int
}

// SystemEnv returns the real environment.
func SystemEnv() Env {
	return Env{
		Getenv: os.Getenv,
		FileExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		GOOS: runtime.GOOS,
		UID:  os.Getuid(),
	}
}

// containerMarkers are files that container runtimes create.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// skipKeyring reports whether the keyring should not be tried at all, and
// why. These environments never have a usable keyring, and on Linux
// without a session bus godbus would autolaunch dbus-launch and leave a
// daemon behind.
func (e Env) skipKeyring() (reason string, skip bool) {
	if e.Getenv("CI") != "" {
		return ReasonCI, true
	}
	if e.Getenv("container") != "" {
		return ReasonContainer, true
	}
	for _, m := range containerMarkers {
		if e.FileExists(m) {
			return ReasonContainer, true
		}
	}
	if e.GOOS == "linux" && !e.hasSessionBus() {
		return ReasonNoDBus, true
	}
	return "", false
}

// hasSessionBus mirrors how godbus finds the session bus without
// autolaunching: DBUS_SESSION_BUS_ADDRESS, then $XDG_RUNTIME_DIR/bus, then
// /run/user/<uid>/bus.
func (e Env) hasSessionBus() bool {
	if addr := e.Getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
		if path, ok := strings.CutPrefix(addr, "unix:path="); ok {
			path, _, _ = strings.Cut(path, ",")
			return e.FileExists(path)
		}
		return true
	}
	if dir := e.Getenv("XDG_RUNTIME_DIR"); dir != "" && e.FileExists(filepath.Join(dir, "bus")) {
		return true
	}
	return e.FileExists("/run/user/" + strconv.Itoa(e.UID) + "/bus")
}
