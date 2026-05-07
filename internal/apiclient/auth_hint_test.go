package apiclient

import (
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// setupCleanCredStores wires the test against an empty mock keyring and
// a temp config dir so neither the real OS keyring nor the user's
// credentials file is touched. macOS's os.UserConfigDir ignores
// XDG_CONFIG_HOME, so we skip there — the file path can't be redirected
// from outside the credential package.
func setupCleanCredStores(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("os.UserConfigDir is not redirectable on darwin; cannot isolate credentials file")
	}
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func clearAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DIBBLA_API_TOKEN", "")
	t.Setenv("DIBBLA_API_URL", "")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "")
}

// TestAuthShadowHint_NoEnv covers the cheap short-circuit: with no
// DIBBLA_* env vars set, the hint must always be empty regardless of
// what's saved.
func TestAuthShadowHint_NoEnv(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)

	if err := credential.SetTokenFile("ak_saved", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	if got := AuthShadowHint(); got != "" {
		t.Errorf("expected empty hint with no env vars, got %q", got)
	}
}

// TestAuthShadowHint_NoSavedCreds covers the other short-circuit: env
// is set but nothing is saved on disk, so there's no shadowing — the
// env IS the configured auth, not an override.
func TestAuthShadowHint_NoSavedCreds(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "ak_env")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.net")

	if got := AuthShadowHint(); got != "" {
		t.Errorf("expected empty hint with no saved creds, got %q", got)
	}
}

// TestAuthShadowHint_TokenMismatch is the headline case — the
// stale-tmux-env footgun. Env token differs from the saved file token,
// so the hint must call out DIBBLA_API_TOKEN by name.
func TestAuthShadowHint_TokenMismatch(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "ak_env_stale")
	t.Setenv("DIBBLA_API_URL", "")

	if err := credential.SetTokenFile("ak_saved_fresh", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	got := AuthShadowHint()
	if got == "" {
		t.Fatal("expected non-empty hint")
	}
	if !strings.Contains(got, "DIBBLA_API_TOKEN") {
		t.Errorf("hint should name DIBBLA_API_TOKEN: %q", got)
	}
	if !strings.Contains(got, "unset DIBBLA_API_TOKEN") {
		t.Errorf("hint should suggest `unset DIBBLA_API_TOKEN`: %q", got)
	}
	// Don't surface DIBBLA_API_URL when only the token diverges.
	if strings.Contains(got, "DIBBLA_API_URL") {
		t.Errorf("hint should not mention DIBBLA_API_URL when only token diverges: %q", got)
	}
}

// TestAuthShadowHint_URLMismatch covers env URL diverging from saved URL,
// with the token unchanged.
func TestAuthShadowHint_URLMismatch(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.net")

	if err := credential.SetTokenFile("ak_saved", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	got := AuthShadowHint()
	if got == "" {
		t.Fatal("expected non-empty hint")
	}
	if !strings.Contains(got, "DIBBLA_API_URL") {
		t.Errorf("hint should name DIBBLA_API_URL: %q", got)
	}
	if strings.Contains(got, "DIBBLA_API_TOKEN") {
		t.Errorf("hint should not mention DIBBLA_API_TOKEN when only URL diverges: %q", got)
	}
}

// TestAuthShadowHint_AuthServiceURLFallback covers DIBBLA_AUTH_SERVICE_URL
// — only flagged when DIBBLA_API_URL is unset (because that's the only
// case where the steprunner-injected env var actually shadows).
func TestAuthShadowHint_AuthServiceURLFallback(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_URL", "")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "https://auth.dibbla.net")

	if err := credential.SetTokenFile("ak_saved", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	got := AuthShadowHint()
	if got == "" {
		t.Fatal("expected non-empty hint")
	}
	if !strings.Contains(got, "DIBBLA_AUTH_SERVICE_URL") {
		t.Errorf("hint should name DIBBLA_AUTH_SERVICE_URL: %q", got)
	}
}

// TestAuthShadowHint_AuthServiceURLIgnoredWhenAPIURLSet ensures we don't
// surface AUTH_SERVICE_URL when API_URL is set (since API_URL wins in
// resolution and AUTH_SERVICE_URL is irrelevant noise).
func TestAuthShadowHint_AuthServiceURLIgnoredWhenAPIURLSet(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "https://auth.dibbla.net")
	t.Setenv("DIBBLA_API_TOKEN", "")

	if err := credential.SetTokenFile("ak_saved", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	got := AuthShadowHint()
	// API URL matches saved URL, so no diff. AUTH_SERVICE_URL must be ignored.
	if got != "" {
		t.Errorf("expected empty hint (API URL matches saved, AUTH_SERVICE_URL is irrelevant), got %q", got)
	}
}

// TestAuthShadowHint_EnvMatchesSaved confirms that env vars matching
// the saved values are NOT flagged — the env is harmless when it
// agrees with what's on disk.
func TestAuthShadowHint_EnvMatchesSaved(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "ak_same")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")

	if err := credential.SetTokenFile("ak_same", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	if got := AuthShadowHint(); got != "" {
		t.Errorf("expected empty hint when env matches saved, got %q", got)
	}
}

// TestAuthShadowHint_TrailingSlashIgnored ensures a stray trailing
// slash doesn't trigger a false "URL diverges" hint.
func TestAuthShadowHint_TrailingSlashIgnored(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "ak_same")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com/")

	if err := credential.SetTokenFile("ak_same", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	if got := AuthShadowHint(); got != "" {
		t.Errorf("expected empty hint despite trailing slash, got %q", got)
	}
}

// TestAuthShadowHint_EmptySavedURLTreatedAsDefault covers the case
// where `dibbla login` stored an empty URL (because the user logged
// into the default API endpoint) and env DIBBLA_API_URL points at a
// different host. The hint must still fire — empty saved URL means
// "default", so a non-default env URL is a real divergence.
func TestAuthShadowHint_EmptySavedURLTreatedAsDefault(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.net")

	// File holds a token but no URL — the on-disk shape `dibbla login`
	// produces when the user logs into the default endpoint.
	if err := credential.SetTokenFile("ak_saved", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	got := AuthShadowHint()
	if got == "" {
		t.Fatal("expected non-empty hint when env URL differs from default")
	}
	if !strings.Contains(got, "DIBBLA_API_URL") {
		t.Errorf("hint should name DIBBLA_API_URL: %q", got)
	}
}

// TestAuthShadowHint_KeyringPreferredOverFile mirrors config.Load's
// resolution: when the keyring has a token, it's the saved value to
// compare against, even if the file also has one with a different
// value.
func TestAuthShadowHint_KeyringPreferredOverFile(t *testing.T) {
	setupCleanCredStores(t)
	clearAuthEnv(t)
	t.Setenv("DIBBLA_API_TOKEN", "ak_keyring")

	// Keyring has the token the env matches; file has a stale one.
	if err := credential.SetToken("ak_keyring"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if err := credential.SetTokenFile("ak_file_stale", "https://api.dibbla.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	if got := AuthShadowHint(); got != "" {
		t.Errorf("expected empty hint (env matches keyring), got %q", got)
	}
}
