package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Choosing a credential store (DIB-1016) ----------------------------------
//
// There are two stores: the OS keyring and a per-context credentials file. On
// macOS and Windows the keyring is always there and always works, so the choice
// is not interesting. On Linux it is the whole problem, and this file is where
// it is decided.
//
// The rule is: do not ask a question whose answer is going to be a failure.
//
// go-keyring reaches the keyring through godbus, and godbus resolves a session
// bus address by (1) $DBUS_SESSION_BUS_ADDRESS, (2) /run/user/<uid>/bus or
// /run/user/<uid>/dbus-session, and failing both (3) *shelling out to
// dbus-launch*, which on a host that has dbus-x11 installed SPAWNS A NEW
// dbus-daemon. On a headless server that daemon can never lead anywhere —
// nothing implements org.freedesktop.secrets there — so every `dibbla` command
// paid for a forked daemon to reach a guaranteed failure, and the failure then
// had to be recognised by matching on another library's error text.
//
// So: probe for the same address godbus would find, minus the autolaunch step.
// No address means no keyring, decided without touching dbus at all.
//
// This is deliberately not XDG_RUNTIME_DIR, even though that is the "correct"
// spelling: godbus hardcodes /run/user/<uid> (conn_other.go:79), and a probe
// that looked somewhere godbus does not would answer a different question than
// the one being asked.

// ErrKeyringUnavailable is returned by the guarded keyring operations when
// this host has no usable keyring — either the probe found no session bus, or
// the operation timed out. Callers test it with errors.Is rather than by
// matching error text; see IsKeyringUnavailable.
var ErrKeyringUnavailable = errors.New("no usable OS keyring on this host")

// EnvStore names the environment variable that overrides the choice below.
//
// It exists because the probe is a heuristic and heuristics are wrong
// sometimes, and when it is wrong the user currently has no way to say so. The
// values are "keyring" (always use it, fail loudly if it does not work),
// "file" (never touch dbus) and "auto" (the default: probe).
const EnvStore = "DIBBLA_CREDENTIAL_STORE"

// Keyring calls are time-boxed on Linux only.
//
// The read budget is short because a read happens on every single command and
// a slow keyring is indistinguishable from a broken one at that cadence. The
// write budget is longer because a write is a deliberate, once-per-login act.
//
// Both exist because go-keyring's unlock path blocks on an unbuffered channel
// receive with no timeout (secret_service.go:208: `signal := <-promptSignal`).
// On a host where a secret service IS registered but the collection is locked
// and nothing can draw a prompt — gnome-keyring on a box entered by SSH public
// key, so pam_gnome_keyring never got a password to unlock it with — that
// receive never completes and `dibbla login` hangs forever, after the token has
// already been validated. A timeout is the only defence available from out
// here.
//
// macOS and Windows are NOT time-boxed: there the OS store may legitimately
// prompt a human, and a human is allowed to take longer than ten seconds to
// find their password.
const (
	keyringReadTimeout  = 2 * time.Second
	keyringWriteTimeout = 10 * time.Second
)

// Store names which of the two stores a credential lives in.
type Store string

const (
	StoreKeyring Store = "keyring"
	StoreFile    Store = "file"
)

// PlaintextWarning is said every time a credential is written to the file
// store, and is a constant rather than a sentence at one call site so that no
// path can quietly stop saying it.
//
// The file is mode 0600 in a 0700 directory, which keeps out other users on
// the host and nothing else. It does not keep out root, a backup, a disk
// image, or — the one that actually bites — anything at all running as this
// same user: a postinstall script, a CI step, a dependency in a repo the user
// happens to cd into. Encrypting it here would not change that, because on an
// unattended host the key would have to sit next to the file; that is
// obfuscation, and worse, it is obfuscation that would let us write the word
// "encrypted" in the docs. So the file is plaintext and we say so.
//
// A host that wants better than this wants DIBBLA_API_TOKEN supplied per
// invocation — systemd LoadCredential, a secrets manager, the CI store —
// which config.Load honours before any of this code runs.
const PlaintextWarning = "this file holds the token in plaintext (mode 0600); on a server prefer DIBBLA_API_TOKEN from your secrets manager"

// storePreference returns the normalised value of $DIBBLA_CREDENTIAL_STORE.
// An unset or unrecognised value reads as "auto"; ValidateStorePreference is
// what turns a typo into a visible error, at the one moment a user is in a
// position to fix it.
func storePreference() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvStore)))
	switch v {
	case string(StoreKeyring), string(StoreFile), "auto":
		return v
	default:
		return "auto"
	}
}

// ValidateStorePreference reports a $DIBBLA_CREDENTIAL_STORE value that is set
// but not one of the three accepted words.
//
// Silently treating "fil" as "auto" would be the worst of both worlds: the user
// believes they have pinned the store and the CLI quietly did something else.
// Commands that care (login, status) call this and refuse; everything else
// keeps working on the default, because a mistyped preference is not a reason
// for `dibbla apps list` to stop.
func ValidateStorePreference() error {
	raw := strings.TrimSpace(os.Getenv(EnvStore))
	if raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case string(StoreKeyring), string(StoreFile), "auto":
		return nil
	}
	return fmt.Errorf("%s=%q is not a recognised value: use %q, %q or %q",
		EnvStore, raw, "auto", StoreKeyring, StoreFile)
}

// keyringUsable memoises the probe for the life of the process.
//
// A plain var behind a mutex rather than a sync.Once, for the reason recorded
// at internal/config.migrateIfNeeded: a Once caches "already decided" across a
// whole test binary, where the second test to care would silently inherit the
// first one's host. The explicit reset below is what tests use instead.
var (
	usableMu sync.Mutex
	// usableForce is set only by SetKeyringUsableForTest. It is separate from
	// usableMemo, and consulted before everything else, because a test on a
	// macOS or Windows developer machine has to be able to simulate a headless
	// Linux host — and the platform shortcut below would otherwise answer
	// first and make that impossible.
	usableForce *bool
	// usableMemo caches the probe. Linux only; nothing else probes.
	usableMemo *bool
)

// KeyringUsable reports whether a keyring operation is worth attempting on
// this host. It never contacts dbus; see the package comment above.
func KeyringUsable() bool {
	usableMu.Lock()
	if usableForce != nil {
		v := *usableForce
		usableMu.Unlock()
		return v
	}
	usableMu.Unlock()

	switch storePreference() {
	case string(StoreFile):
		return false
	case string(StoreKeyring):
		return true
	}

	// macOS Keychain and Windows Credential Manager are part of the OS and
	// need no probing.
	if runtime.GOOS != "linux" {
		return true
	}

	usableMu.Lock()
	defer usableMu.Unlock()
	if usableMemo != nil {
		return *usableMemo
	}
	v := sessionBusAddressable()
	usableMemo = &v
	return v
}

// SetKeyringUsableForTest forces the answer KeyringUsable gives, and returns a
// function that restores the previous state.
//
// Exported because the CI runner is Linux and has no session bus: without this
// every test that exercises the keyring path would short-circuit to the file
// store and quietly stop testing what it claims to test — a green suite that
// covers nothing. credtest.Install calls it.
func SetKeyringUsableForTest(v bool) (restore func()) {
	usableMu.Lock()
	defer usableMu.Unlock()
	previous := usableForce
	usableForce = &v
	return func() {
		usableMu.Lock()
		defer usableMu.Unlock()
		usableForce = previous
	}
}

// sessionBusAddressable mirrors godbus's getSessionBusAddress, minus the
// dbus-launch autolaunch fallback that we specifically do not want.
func sessionBusAddressable() bool {
	// godbus ignores the literal "autolaunch:" here and falls through to
	// discovery, so we do too — an address of "autolaunch:" is a request to
	// spawn a daemon, not evidence that one is running.
	if a := strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")); a != "" && a != "autolaunch:" {
		return true
	}
	uid := os.Getuid()
	if uid < 0 {
		// Windows; unreachable given the GOOS check above, but Getuid's
		// contract is -1 there and a negative uid must not become a path.
		return false
	}
	runtimeDir := filepath.Join("/run", "user", strconv.Itoa(uid))
	for _, name := range []string{"bus", "dbus-session"} {
		if _, err := os.Stat(filepath.Join(runtimeDir, name)); err == nil {
			return true
		}
	}
	return false
}

// ResetKeyringProbeForTest clears the memoised probe result.
func ResetKeyringProbeForTest() {
	usableMu.Lock()
	defer usableMu.Unlock()
	usableMemo, usableForce = nil, nil
}

// --- The guarded operations --------------------------------------------------
//
// Everything in this package that touches the keyring goes through these three
// rather than calling the KeyringGet/Set/Delete seams directly, so the probe
// and the timeout cannot be forgotten at a call site.

func guardedGet(key string) (string, error) {
	if !KeyringUsable() {
		return "", ErrKeyringUnavailable
	}
	return withTimeout(keyringReadTimeout, func() (string, error) {
		return KeyringGet(serviceName, key)
	})
}

func guardedSet(key, value string) error {
	if !KeyringUsable() {
		return ErrKeyringUnavailable
	}
	_, err := withTimeout(keyringWriteTimeout, func() (string, error) {
		return "", KeyringSet(serviceName, key, value)
	})
	return err
}

func guardedDelete(key string) error {
	if !KeyringUsable() {
		return ErrKeyringUnavailable
	}
	_, err := withTimeout(keyringWriteTimeout, func() (string, error) {
		return "", KeyringDelete(serviceName, key)
	})
	return err
}

// withTimeout runs fn on its own goroutine and gives up waiting after d,
// returning ErrKeyringUnavailable.
//
// On Linux only: see the timeout constants above for why the other platforms
// are left alone.
//
// The goroutine is deliberately left running when the timeout fires. It is
// blocked inside cgo or on a dbus channel receive and there is no way to cancel
// it from here; the alternative to leaking it is to block on it, which is the
// bug being fixed. The CLI is a short-lived process and the leak dies with it.
func withTimeout(d time.Duration, fn func() (string, error)) (string, error) {
	if runtime.GOOS != "linux" {
		return fn()
	}
	type result struct {
		v   string
		err error
	}
	ch := make(chan result, 1) // buffered: the abandoned goroutine must not block forever on send
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-timer.C:
		return "", fmt.Errorf("the OS keyring did not respond within %s: %w", d, ErrKeyringUnavailable)
	}
}
