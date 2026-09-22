package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/joho/godotenv"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
	"github.com/dibbla-agents/dibbla-cli/internal/env"
)

// User-level credentials file. Used as a fallback when the OS keyring
// is unavailable (typical on Linux SSH/cloud-VM/Docker hosts where
// libsecret/gnome-keyring isn't installed). Mirrors keychain semantics
// — machine-wide, persists across `cd` — rather than the cwd-bound
// behavior of `--write-env`.

const (
	fileTokenKey   = "DIBBLA_API_TOKEN"
	fileAPIURLKey  = "DIBBLA_API_URL"
	fileOrgIDKey   = "DIBBLA_ORG_ID"
	fileOrgNameKey = "DIBBLA_ORG_NAME"
	credFileName   = "credentials.env"
)

// tokenFilePath resolves the legacy credentials file path. Tests isolate it
// through cfgdir.SetForTest rather than through XDG_CONFIG_HOME, which
// os.UserConfigDir ignores on macOS.
func tokenFilePath() string {
	return cfgdir.Join(credFileName)
}

// contextFilePath resolves a context's own credentials file,
// credentials.<name>.env. Returns "" when the config dir cannot be resolved or
// the name is unusable as a filename.
//
// The name is validated here rather than trusted: config.yaml is a
// hand-editable file, so a context name is user input, and a name containing a
// path separator would otherwise write a credentials file — mode 0600, holding
// a bearer token — at an arbitrary path outside the config directory. The
// permitted set matches contextcfg.ValidName; it is duplicated rather than
// imported because contextcfg sits above this package and importing it here
// would invert the dependency.
func contextFilePath(name string) string {
	if !validContextFileName(name) {
		return ""
	}
	return cfgdir.Join("credentials." + name + ".env")
}

func validContextFileName(name string) bool {
	if name == "" || len(name) > 64 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
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
	vars, err := readCredFile()
	if err != nil {
		return "", "", err
	}
	return vars[fileTokenKey], vars[fileAPIURLKey], nil
}

// readCredFile parses the legacy credentials file into a key/value map. A
// missing file yields an empty map and no error — callers treat that as
// "nothing stored" rather than a failure.
func readCredFile() (map[string]string, error) {
	return readCredFileAt(tokenFilePath())
}

// readCredFileAt is readCredFile against an explicit path, so the per-context
// files and the legacy one share one parser.
func readCredFileAt(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	// Refuse a symlink rather than following it. The credentials file is
	// created 0600 in a 0700 directory, but neither mode stops something that
	// got there first from leaving a symlink at the name we are about to read
	// — and then a later MergeEnvFile write would land wherever it points.
	// Lstat is what distinguishes "the file" from "a pointer to a file".
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; refusing to read credentials through it", path)
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()
	vars, err := godotenv.Parse(f)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

// SetOrgFile writes the pinned organization into the user-level credentials
// file, leaving the token and API URL entries untouched. Kept separate from
// SetTokenFile because pinning an org is not a login: the two are written at
// different times and clobbering the token here would be a way to lose it.
func SetOrgFile(orgID, orgName string) error {
	path := tokenFilePath()
	if path == "" {
		return errors.New("could not resolve user config directory for credentials file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	updates := map[string]string{
		fileOrgIDKey:   orgID,
		fileOrgNameKey: orgName,
	}
	if _, err := env.MergeEnvFile(path, updates); err != nil {
		return err
	}
	return nil
}

// GetOrgFile reads the pinned organization from the user-level credentials
// file. Returns ("", "", nil) when the file doesn't exist or holds no pin.
func GetOrgFile() (orgID, orgName string, err error) {
	vars, err := readCredFile()
	if err != nil {
		return "", "", err
	}
	return vars[fileOrgIDKey], vars[fileOrgNameKey], nil
}

// DeleteOrgFile clears the pinned organization from the credentials file. The
// keys are blanked rather than removed — MergeEnvFile has no delete mode, and
// an empty value reads back as "no pin" everywhere it is consumed. No-op when
// the file doesn't exist, so clearing a pin that was never set is not an error.
func DeleteOrgFile() error {
	path := tokenFilePath()
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return SetOrgFile("", "")
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

// IsKeyringAbsent reports whether err means there was no keyring to talk to at
// all — the probe found no session bus, or the call timed out.
//
// This is the strict question, and it is the one REMOVAL asks. Deleting a
// credential from a store that does not exist has succeeded; a delete that was
// attempted and refused has not, and must be reported, because telling someone
// their token is gone while it is still live in their keyring is the one
// outcome `dibbla logout` must never produce.
//
// IsKeyringUnavailable below is the loose question, and it is the one STORAGE
// asks: "should I write to the file instead?" Storage may answer yes to a much
// wider set of failures, because the fallback is harmless — the credential
// still lands somewhere the CLI can read. Removal may not, because its fallback
// is silence.
//
// The two were briefly the same function. On Linux that made `dibbla logout`
// swallow a keyring delete that had genuinely failed, since the loose rule
// treats an unrecognised Linux error as "no keyring here".
func IsKeyringAbsent(err error) bool {
	return err != nil && errors.Is(err, ErrKeyringUnavailable)
}

// IsKeyringUnavailable reports whether err means the file store should be used
// for STORAGE instead of the keyring. See IsKeyringAbsent for the stricter
// question that removal asks, and why they must differ.
//
// It used to be an allowlist of four substrings matched against the error text
// of two external libraries. That was the wrong shape, and it was wrong in
// practice: on a headless Linux box the failure usually happens a layer below
// the one those substrings describe. go-keyring never reaches
// org.freedesktop.secrets because godbus cannot resolve a session bus first,
// and the errors it actually returns are
//
//	exec: "dbus-launch": executable file not found in $PATH
//	dbus: couldn't determine address of session bus
//	dial unix /run/user/1000/bus: connect: no such file or directory
//
// none of which matched. The consequence was that `dibbla login` failed
// outright on exactly the hosts the file fallback was built for. (The fourth
// needle, "could not connect: dial unix", is emitted by neither library — it
// existed only in this package's own test.)
//
// The shape is now: an allowlist of the cases where we KEEP the failure, and
// everything else falls back. The kept cases are the ones where a keyring
// demonstrably exists and a human said no to it — auto-writing a plaintext
// copy of a token there would override a decision the user just made. On
// macOS and Windows nothing falls back, because a keyring failure there is a
// real fault worth surfacing rather than a host that never had one.
func IsKeyringUnavailable(err error) bool {
	if err == nil {
		return false
	}
	// The probe and the timeout in backend.go speak through this sentinel, so
	// no text matching is involved on the paths this package controls.
	if errors.Is(err, ErrKeyringUnavailable) {
		return true
	}

	msg := strings.ToLower(err.Error())

	// A keyring that is present and refused. Not a host without one.
	for _, kept := range []string{"dismissed", "cancel", "denied", "not permitted"} {
		if strings.Contains(msg, kept) {
			return false
		}
	}

	if runtime.GOOS == "linux" {
		// Anything else on Linux: treat as no keyring. Enumerating the ways
		// dbus and libsecret can fail is what produced the bug above, and the
		// list would have to be re-derived every time either dependency moves.
		// Storing a validated token beats refusing to store it.
		return true
	}

	// macOS / Windows: keep the original narrow matching, for the case of a
	// dibbla binary running on a Unix host that is neither.
	for _, n := range []string{
		"org.freedesktop.secrets",  // canonical libsecret-on-DBus error
		"the name org.freedesktop", // partial match for the full DBus message
		"no secret service",        // alternate go-keyring wording
	} {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

// --- Per-context credentials files (P-0011) ---------------------------------
//
// On hosts with no keyring service each context keeps its own
// credentials.<name>.env, so logging in to a second server no longer destroys
// the first. The legacy credentials.env is NOT retired: internal/config mirrors
// the active context into it, which is what keeps a pre-context binary and
// every shipped script that sources that file working unchanged. See
// internal/config/context.go for that rule and why it is a mirror rather than a
// migration.

// SetContextTokenFile writes a context's token and API URL to its own
// credentials file at 0600, creating the config directory at 0700 if needed.
func SetContextTokenFile(name, token, apiURL string) error {
	path := contextFilePath(name)
	if path == "" {
		return fmt.Errorf("cannot resolve a credentials file for context %q", name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if _, err := env.MergeEnvFile(path, map[string]string{
		fileTokenKey:  token,
		fileAPIURLKey: apiURL,
	}); err != nil {
		return err
	}
	return nil
}

// GetContextTokenFile reads a context's token and API URL from its own
// credentials file. Returns ("", "", nil) when the file does not exist.
func GetContextTokenFile(name string) (token, apiURL string, err error) {
	vars, err := readCredFileAt(contextFilePath(name))
	if err != nil {
		return "", "", err
	}
	return vars[fileTokenKey], vars[fileAPIURLKey], nil
}

// DeleteContextTokenFile removes a context's credentials file. No-op when it
// does not exist.
func DeleteContextTokenFile(name string) error {
	path := contextFilePath(name)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ContextTokenFilePath returns the path of a context's credentials file, for
// messages and for the uninstall inventory. Empty when unresolvable.
func ContextTokenFilePath(name string) string {
	return contextFilePath(name)
}

// ListContextTokenFiles returns the context names that have a credentials file
// on this host, sorted. `dibbla uninstall` uses it so a file left behind by a
// context that was hand-deleted from config.yaml is still removed — enumerating
// only what config.yaml lists would leave orphaned tokens on disk.
func ListContextTokenFiles() []string {
	dir := cfgdir.Dir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		// The affixes must not overlap. The legacy credentials.env matches both
		// the prefix and the suffix while containing neither a name nor a
		// separating dot, and a naive TrimPrefix+TrimSuffix turns it into a
		// context called "env" — which `dibbla uninstall` would then walk. Two
		// affixes that meet in the middle are not a match.
		const pre, suf = "credentials.", ".env"
		if len(n) <= len(pre)+len(suf) || !strings.HasPrefix(n, pre) || !strings.HasSuffix(n, suf) {
			continue
		}
		name := n[len(pre) : len(n)-len(suf)]
		if validContextFileName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
