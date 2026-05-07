package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	"github.com/dibbla-agents/dibbla-cli/internal/env"
)

// User-level credentials file. Used as a fallback when the OS keyring
// is unavailable (typical on Linux SSH/cloud-VM/Docker hosts where
// libsecret/gnome-keyring isn't installed). Mirrors keychain semantics
// — machine-wide, persists across `cd` — rather than the cwd-bound
// behavior of `--write-env`.

const (
	fileTokenKey  = "DIBBLA_API_TOKEN"
	fileAPIURLKey = "DIBBLA_API_URL"
	credFileName  = "credentials.env"
)

// tokenFilePath resolves the credentials file path. Overridable in
// tests so they can isolate writes without setting XDG_CONFIG_HOME (on
// macOS, os.UserConfigDir ignores it). Mirrors update.stateFilePath's
// pattern.
var tokenFilePath = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dibbla", credFileName)
}

// TokenFilePath returns the absolute path of the user-level
// credentials file. Empty string if the user config dir cannot be
// resolved (extremely unusual; would mean both $HOME and $XDG_CONFIG_HOME
// are unset on a non-Windows host).
func TokenFilePath() string {
	return tokenFilePath()
}

// SetTokenFile writes token + apiURL to the user-level credentials
// file at 0600. Creates the parent directory at 0700 if needed. Pass
// apiURL="" when the default API URL is in use — an empty value is
// stored and config.Load treats it as "no override," which both
// matches the keychain semantics (DeleteAPIURL when default) and
// ensures a previously-stored custom URL is cleared on re-login.
func SetTokenFile(token, apiURL string) error {
	path := tokenFilePath()
	if path == "" {
		return errors.New("could not resolve user config directory for credentials file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	updates := map[string]string{
		fileTokenKey:  token,
		fileAPIURLKey: apiURL,
	}
	if _, err := env.MergeEnvFile(path, updates); err != nil {
		return err
	}
	return nil
}

// GetTokenFile reads token and apiURL from the user-level credentials
// file. Returns ("", "", nil) if the file doesn't exist — callers
// should treat this as "no stored credentials" rather than an error.
func GetTokenFile() (token, apiURL string, err error) {
	path := tokenFilePath()
	if path == "" {
		return "", "", nil
	}
	f, ferr := os.Open(path)
	if ferr != nil {
		if os.IsNotExist(ferr) {
			return "", "", nil
		}
		return "", "", ferr
	}
	defer f.Close()
	vars, perr := godotenv.Parse(f)
	if perr != nil {
		return "", "", perr
	}
	return vars[fileTokenKey], vars[fileAPIURLKey], nil
}

// DeleteTokenFile removes the user-level credentials file. No-op if it
// doesn't exist or the path can't be resolved.
func DeleteTokenFile() error {
	path := tokenFilePath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsKeyringUnavailable reports whether err indicates the OS keyring
// service is not running on this host (vs. some other keyring failure
// like a denied unlock prompt or a malformed entry). On Linux this
// matches the wording libsecret/D-Bus produces when neither
// gnome-keyring nor KWallet provides the org.freedesktop.secrets
// service. Used to decide whether to fall back to file-based
// credential storage — we only auto-fallback when the keyring is
// genuinely absent, not when the user actively rejected it.
func IsKeyringUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		"org.freedesktop.secrets",      // canonical libsecret-on-DBus error
		"the name org.freedesktop",     // partial match for the full DBus message
		"no secret service",            // alternate go-keyring wording
		"could not connect: dial unix", // DBus socket missing entirely
	}
	for _, n := range needles {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}
