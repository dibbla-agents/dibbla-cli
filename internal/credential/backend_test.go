package credential

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// clearProbeEnv gives a test a clean slate: no store preference, no inherited
// session bus address, and no memoised probe result from an earlier test in
// this binary.
func clearProbeEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvStore, "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	ResetKeyringProbeForTest()
	t.Cleanup(ResetKeyringProbeForTest)
}

func TestStorePreference_OverridesTheProbe(t *testing.T) {
	clearProbeEnv(t)

	t.Setenv(EnvStore, "file")
	if KeyringUsable() {
		t.Error(`DIBBLA_CREDENTIAL_STORE=file must never use the keyring`)
	}
	ResetKeyringProbeForTest()

	t.Setenv(EnvStore, "keyring")
	if !KeyringUsable() {
		t.Error(`DIBBLA_CREDENTIAL_STORE=keyring must use the keyring even where the probe says no`)
	}
}

// The override is what a user reaches for when the probe guesses wrong, so a
// typo in it must not silently resolve to the default — that is the failure
// mode where someone believes they have pinned the store and has not.
func TestValidateStorePreference(t *testing.T) {
	clearProbeEnv(t)

	for _, ok := range []string{"", "auto", "file", "keyring", "KEYRING", "  file  "} {
		t.Setenv(EnvStore, ok)
		if err := ValidateStorePreference(); err != nil {
			t.Errorf("%s=%q must be accepted: %v", EnvStore, ok, err)
		}
	}
	for _, bad := range []string{"fil", "plaintext", "yes", "keychain"} {
		t.Setenv(EnvStore, bad)
		if err := ValidateStorePreference(); err == nil {
			t.Errorf("%s=%q must be rejected rather than silently read as auto", EnvStore, bad)
		}
	}
}

// The probe is the fix for the bug this work exists to close: on a headless
// host the CLI must decide "no keyring" without ever contacting dbus, because
// contacting dbus is what forks a dbus-daemon and then fails anyway.
func TestSessionBusProbe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the probe only runs on linux; every other host is assumed to have an OS keyring")
	}
	clearProbeEnv(t)

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	if !sessionBusAddressable() {
		t.Error("an explicit DBUS_SESSION_BUS_ADDRESS is a session bus")
	}

	// godbus treats the literal "autolaunch:" as a request to SPAWN a bus,
	// not as evidence of one, and falls through to discovery. So must we —
	// honouring it would reintroduce the forked daemon on every command.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "autolaunch:")
	uid := strconv.Itoa(os.Getuid())
	_, busErr := os.Stat(filepath.Join("/run/user", uid, "bus"))
	_, sessErr := os.Stat(filepath.Join("/run/user", uid, "dbus-session"))
	hostHasOne := busErr == nil || sessErr == nil
	if got := sessionBusAddressable(); got != hostHasOne {
		t.Errorf("autolaunch: fell back to discovery = %v, want %v (this host has a socket: %v)",
			got, hostHasOne, hostHasOne)
	}
}

func TestKeyringUsable_NonLinuxAlwaysTrue(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux is the platform that probes")
	}
	clearProbeEnv(t)
	if !KeyringUsable() {
		t.Errorf("%s has an OS credential store; it must not be probed away", runtime.GOOS)
	}
}

// An unusable keyring must not reach the seams at all. This is the property
// that stops `dibbla` forking a dbus-daemon per invocation on a host with
// dbus-x11 but no secret service.
func TestGuardedOps_DoNotTouchTheKeyringWhenUnusable(t *testing.T) {
	clearProbeEnv(t)
	t.Cleanup(SetKeyringUsableForTest(false))

	reached := false
	g, s, d := KeyringGet, KeyringSet, KeyringDelete
	KeyringGet = func(_, _ string) (string, error) { reached = true; return "", nil }
	KeyringSet = func(_, _, _ string) error { reached = true; return nil }
	KeyringDelete = func(_, _ string) error { reached = true; return nil }
	t.Cleanup(func() { KeyringGet, KeyringSet, KeyringDelete = g, s, d })

	if _, err := guardedGet("k"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("guardedGet err = %v, want ErrKeyringUnavailable", err)
	}
	if err := guardedSet("k", "v"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("guardedSet err = %v, want ErrKeyringUnavailable", err)
	}
	if err := guardedDelete("k"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("guardedDelete err = %v, want ErrKeyringUnavailable", err)
	}
	if reached {
		t.Error("an unusable keyring was contacted anyway — the whole point of the probe is that it is not")
	}
}

// A read through the public API must report "nothing stored" rather than an
// error when there is no keyring, so every caller falls through to the
// credentials file instead of having to distinguish absent from broken.
func TestGet_UnavailableKeyringReadsAsEmpty(t *testing.T) {
	withTempCredFile(t) // never the developer's own config dir
	clearProbeEnv(t)
	t.Cleanup(SetKeyringUsableForTest(false))

	tok, err := GetContextToken("prod")
	if err != nil || tok != "" {
		t.Errorf("GetContextToken on a keyring-less host = (%q, %v), want (\"\", nil)", tok, err)
	}
}

// The regression that motivated the timeout. go-keyring's unlock path blocks
// on an unbuffered channel receive with no deadline, so a locked collection
// with nothing able to draw a prompt hangs `dibbla login` forever — after the
// token has already been validated. The guard must give up instead.
func TestWithTimeout_GivesUpOnAKeyringThatNeverAnswers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only linux is time-boxed; elsewhere the OS store may legitimately prompt a human")
	}

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	start := time.Now()
	_, err := withTimeout(50*time.Millisecond, func() (string, error) {
		<-blocked // exactly what secret_service.go:208 does
		return "", nil
	})
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("err = %v, want ErrKeyringUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waited %s for a keyring that never answers", elapsed)
	}
	// And a timeout must route to the file store, not to a hard failure.
	if !IsKeyringUnavailable(err) {
		t.Error("a timed-out keyring must fall back to the file store")
	}
}

func TestWithTimeout_PassesThroughTheNormalAnswer(t *testing.T) {
	v, err := withTimeout(5*time.Second, func() (string, error) { return "tok", nil })
	if v != "tok" || err != nil {
		t.Errorf("withTimeout = (%q, %v), want (\"tok\", nil)", v, err)
	}

	boom := errors.New("keyring said no")
	if _, err := withTimeout(5*time.Second, func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying error preserved", err)
	}
}
