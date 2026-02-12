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

// Icon returns emoji on modern terminals, ASCII fallback on legacy Windows consoles.
func Icon(emoji, fallback string) string {
	if SupportsUnicode() {
		return emoji
	}
	return fallback
}
