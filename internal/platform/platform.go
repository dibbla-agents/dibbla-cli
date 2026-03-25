package platform

import (
	"os"
	"runtime"
	"strings"
)

// SupportsUnicode returns true if the terminal likely supports Unicode emoji.
func SupportsUnicode() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	// Windows Terminal, VSCode, ConEmu all support Unicode
	if os.Getenv("WT_SESSION") != "" ||
		strings.Contains(os.Getenv("TERM_PROGRAM"), "vscode") ||
		os.Getenv("ConEmuPID") != "" {
		return true
	}
	return false
}

// IsCI returns true when running in a CI environment.
func IsCI() bool {
	return os.Getenv("CI") != "" ||
		os.Getenv("GITHUB_ACTIONS") != "" ||
		os.Getenv("GITLAB_CI") != "" ||
		os.Getenv("JENKINS_HOME") != "" ||
		os.Getenv("BUILDKITE") != ""
}

// Icon returns emoji on modern terminals, ASCII fallback on legacy Windows consoles.
func Icon(emoji, fallback string) string {
	if SupportsUnicode() {
		return emoji
	}
	return fallback
}
