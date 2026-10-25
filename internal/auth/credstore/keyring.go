package credstore

import (
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// Keyring is the system keyring API, as provided by go-keyring.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

// SystemKeyring returns the OS keyring: Keychain on macOS, Credential
// Manager on Windows, the Secret Service over D-Bus on Linux.
func SystemKeyring() Keyring { return systemKeyring{} }

type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }

func (systemKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (systemKeyring) Delete(service, user string) error { return keyring.Delete(service, user) }

// Timeouts for keyring calls. go-keyring has no timeout and blocks forever
// on an unlock prompt, so every call is bounded: briefly when nobody can
// answer a prompt, long enough to answer one at a terminal.
const (
	TimeoutNonInteractive = 3 * time.Second
	TimeoutInteractive    = 60 * time.Second
)

// ErrKeyringTimeout means a keyring call did not return in time.
var ErrKeyringTimeout = errors.New("keyring timed out")

// UnavailableError means the keyring could not be used.
type UnavailableError struct {
	// Reason is a short explanation shown to the user.
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return "system keyring unavailable: " + e.Reason
	}
	return "system keyring unavailable (" + e.Reason + "): " + e.Err.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// boundedKeyring bounds every call with a timeout and maps errors:
// go-keyring's ErrNotFound becomes ErrNotFound and every other error
// becomes an *UnavailableError.
type boundedKeyring struct {
	k       Keyring
	timeout time.Duration
}

func (b boundedKeyring) get(service, user string) (string, error) {
	var secret string
	err := b.call(func() error {
		var err error
		secret, err = b.k.Get(service, user)
		return err
	})
	return secret, err
}

func (b boundedKeyring) set(service, user, secret string) error {
	return b.call(func() error { return b.k.Set(service, user, secret) })
}

func (b boundedKeyring) delete(service, user string) error {
	return b.call(func() error { return b.k.Delete(service, user) })
}

// call runs fn with the timeout. On timeout the goroutine is abandoned;
// the CLI exits soon after, and go-keyring offers no way to cancel it.
func (b boundedKeyring) call(fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()

	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	var err error
	select {
	case err = <-done:
	case <-timer.C:
		err = ErrKeyringTimeout
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	}
	return &UnavailableError{Reason: reasonFor(err), Err: err}
}

// reasonFor classifies a keyring error. go-keyring returns D-Bus, exec and
// plain string errors that have no sentinels, so this matches on text.
func reasonFor(err error) string {
	if errors.Is(err, ErrKeyringTimeout) {
		return ReasonTimeout
	}
	if errors.Is(err, keyring.ErrSetDataTooBig) {
		return "credential too large for the keyring"
	}
	if _, ok := errors.AsType[*exec.Error](err); ok {
		return ReasonNoDBus
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "org.freedesktop.secrets"),
		strings.Contains(msg, "serviceunknown"),
		strings.Contains(msg, "secret service"):
		return ReasonNoSecretService
	case strings.Contains(msg, "locked"), strings.Contains(msg, "prompt"):
		return ReasonLocked
	case strings.Contains(msg, "dbus"), strings.Contains(msg, "d-bus"):
		return ReasonNoDBus
	}
	return "keyring error"
}
