// Package cfgdir resolves the CLI's per-user configuration directory —
// ~/.config/dibbla on Linux, ~/Library/Application Support/dibbla on macOS,
// %AppData%\dibbla on Windows.
//
// It exists as its own package for one reason: it is the single seam through
// which tests isolate every on-disk artefact the CLI owns. Before named
// contexts there was one such artefact (credentials.env) and one unexported
// override in internal/credential. There are now three kinds — the credentials
// files, the context list, and the update-notifier state — read by three
// packages, and a test that isolates only one of them writes into the
// developer's real config directory with the other two.
//
// The override is exported deliberately. os.UserConfigDir ignores
// XDG_CONFIG_HOME on macOS, so a test in another package cannot isolate this
// directory by setting an environment variable; without an exported seam such
// tests either pollute the real config dir or get skipped on macOS, and a
// silently skipped test is indistinguishable from a passing one.
package cfgdir

import (
	"os"
	"path/filepath"
)

// override, when non-empty, replaces the resolved directory. Set only through
// SetForTest.
var override string

// Dir returns the dibbla config directory. Empty string when the user config
// directory cannot be resolved at all — which would mean both $HOME and
// $XDG_CONFIG_HOME are unset on a non-Windows host. Callers treat an empty
// path as "no persistent storage available" rather than as an error.
func Dir() string {
	if override != "" {
		return override
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dibbla")
}

// Join returns a path inside the config directory, or "" if the directory
// cannot be resolved — so callers get one thing to check rather than two.
func Join(elem ...string) string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(append([]string{dir}, elem...)...)
}

// SetForTest points the config directory at dir and returns a function that
// restores the previous value. Intended for t.Cleanup:
//
//	t.Cleanup(cfgdir.SetForTest(t.TempDir()))
func SetForTest(dir string) (restore func()) {
	previous := override
	override = dir
	return func() { override = previous }
}
