// Package env provides helpers for writing to .env files and patching
// .gitignore entries. Used by `dibbla login --write-env` to materialize
// credentials in a project's working directory without disturbing other
// keys, comments, or ordering in existing files.
package env

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// MergeEnvFile writes the given key/value pairs into envPath while preserving
// the file's existing content — other keys, comments, blank lines, and
// line-ordering survive unchanged. Keys that already exist are replaced
// in place; new keys are appended at the end of the file.
//
// On Unix the file is written with 0600 permissions. On Windows permissions
// fall through to the default NTFS ACL inherited from the user profile.
//
// Writes are atomic: we write to envPath + ".tmp" in the same directory
// and rename over envPath on success. On failure the original file is left
// untouched.
//
// The returned slice lists the key names that were written (in input-map
// iteration order), suitable for surfacing in a CLI confirmation message.
func MergeEnvFile(envPath string, updates map[string]string) ([]string, error) {
	if len(updates) == 0 {
		return nil, nil
	}

	var originalBytes []byte
	if data, err := os.ReadFile(envPath); err == nil {
		originalBytes = data
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", envPath, err)
	}

	originalEndedWithNewline := len(originalBytes) > 0 && originalBytes[len(originalBytes)-1] == '\n'

	// Split carefully: strings.Split on "\n" of a trailing-newline file
	// produces a blank trailing element that we must not re-emit verbatim
	// (otherwise every rewrite adds a blank line).
	text := string(originalBytes)
	if originalEndedWithNewline {
		text = strings.TrimSuffix(text, "\n")
	}
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}

	written := make([]string, 0, len(updates))
	remaining := make(map[string]string, len(updates))
	for k, v := range updates {
		remaining[k] = v
	}

	for i, line := range lines {
		key, ok := parseEnvKey(line)
		if !ok {
			continue
		}
		if val, found := remaining[key]; found {
			lines[i] = formatLine(key, val)
			written = append(written, key)
			delete(remaining, key)
		}
	}

	// Append the new keys in sorted order so two pulls of the same set
	// produce the same file (and a diff of .env.local reads as a diff).
	pending := make([]string, 0, len(remaining))
	for k := range remaining {
		pending = append(pending, k)
	}
	sort.Strings(pending)
	for _, k := range pending {
		lines = append(lines, formatLine(k, updates[k]))
		written = append(written, k)
	}

	out := strings.Join(lines, "\n")
	if len(lines) > 0 {
		out += "\n"
	}

	if err := atomicWrite(envPath, []byte(out), 0600); err != nil {
		return nil, err
	}
	return written, nil
}

// EnsureGitignoreEntry guarantees that gitignorePath contains a line
// exactly matching ".env" or "/.env" (case-sensitive, whitespace-trimmed).
// If the file doesn't exist it's created containing ".env\n". If it exists
// and already has the entry, the function is a no-op. Otherwise ".env" is
// appended (prepended by a newline if the file doesn't already end in one).
//
// Returns true if the file was created or modified, false if it was already
// correct.
func EnsureGitignoreEntry(gitignorePath string) (bool, error) {
	return EnsureGitignoreLine(gitignorePath, ".env")
}

// EnsureGitignoreLine is EnsureGitignoreEntry for any single file name:
// present as `name` or `/name` on its own line means already ignored, and
// otherwise the line is appended. `dibbla env pull` uses it for .env.local
// so the file it writes can never be committed by accident (DIB-919).
func EnsureGitignoreLine(gitignorePath, name string) (bool, error) {
	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", gitignorePath, err)
	}

	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimRight(strings.TrimLeft(line, " \t"), " \t\r")
			if trimmed == name || trimmed == "/"+name {
				return false, nil
			}
		}
	}

	var out []byte
	if len(data) == 0 {
		out = []byte(name + "\n")
	} else {
		out = data
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, (name + "\n")...)
	}

	if err := atomicWrite(gitignorePath, out, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// parseEnvKey returns the KEY portion of a `KEY=VALUE` .env line, or ("", false)
// for blank lines, comments, or malformed content. Leading whitespace is
// permitted but unusual. The key must start with a letter or underscore and
// contain only letters, digits, or underscores — matching the godotenv grammar
// and POSIX env-var conventions.
func parseEnvKey(line string) (string, bool) {
	s := strings.TrimLeft(line, " \t")
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return "", false
	}
	key := strings.TrimRight(s[:eq], " \t")
	if !isEnvKey(key) {
		return "", false
	}
	return key, true
}

func isEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// ParseKey is parseEnvKey for callers outside the package: the KEY of a
// `KEY=VALUE` line, false for blank lines, comments and malformed content.
func ParseKey(line string) (string, bool) { return parseEnvKey(line) }

// WriteFile replaces path with data, atomically and with 0600 permissions on
// Unix — the same guarantees MergeEnvFile gives a file it rewrites.
func WriteFile(path string, data []byte) error { return atomicWrite(path, data, 0600) }

// FormatLine is formatLine for callers outside the package (env pull writes
// whole files and stdout with it).
func FormatLine(key, value string) string { return formatLine(key, value) }

// formatLine renders a KEY=VALUE line. Values without whitespace, quotes,
// backslashes, or shell metacharacters are written raw — matching how the
// steprunner injects env into subprocesses. Values that need escaping are
// double-quoted with minimal backslash escaping.
//
// A value holding `$` is single-quoted when it can be: every dotenv loader
// (godotenv, dotenv-expand, Next.js, Vite) leaves single-quoted values
// unexpanded, while `$` inside double quotes or bare is expanded by several
// of them — and a secret that happens to contain `$FOO` must round-trip
// through `dibbla env pull` byte for byte.
func formatLine(key, value string) string {
	if strings.ContainsRune(value, '$') && !strings.ContainsAny(value, "'\n\r") {
		return key + "='" + value + "'"
	}
	if needsQuoting(value) {
		var b strings.Builder
		b.WriteString(key)
		b.WriteString(`="`)
		for _, r := range value {
			switch r {
			case '\\', '"':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	return key + "=" + value
}

func needsQuoting(v string) bool {
	if v == "" {
		return false
	}
	if v[0] == ' ' || v[0] == '\t' || v[len(v)-1] == ' ' || v[len(v)-1] == '\t' {
		return true
	}
	for _, r := range v {
		switch r {
		case '\n', '\r', '"', '\\', '\'', '#', '$', ' ', '\t':
			return true
		}
	}
	return false
}

// atomicWrite writes data to path by first writing to path+".tmp" in the
// same directory, then renaming. On Unix, chmod is applied after rename.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	tmpName := tmp.Name()
	// On any return path from here, remove the tempfile if it still exists.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tempfile: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows quirk: Rename may fail if dest exists on older FS.
		// Try remove-then-rename as a fallback.
		if runtime.GOOS == "windows" {
			if removeErr := os.Remove(path); removeErr == nil {
				if renameErr := os.Rename(tmpName, path); renameErr == nil {
					goto chmod
				}
			}
		}
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
chmod:
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, perm)
	}
	return nil
}
