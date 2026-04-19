package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMergeEnvFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	written, err := MergeEnvFile(path, map[string]string{
		"DIBBLA_API_TOKEN": "ak_123",
		"DIBBLA_API_URL":   "https://api.dibbla.net",
	})
	if err != nil {
		t.Fatalf("MergeEnvFile: %v", err)
	}
	if len(written) != 2 {
		t.Errorf("written: got %d, want 2", len(written))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "DIBBLA_API_TOKEN=ak_123\n") {
		t.Errorf("token line missing, got:\n%s", got)
	}
	if !strings.Contains(got, "DIBBLA_API_URL=https://api.dibbla.net\n") {
		t.Errorf("url line missing, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("file should end in newline, got:\n%q", got)
	}
}

func TestMergeEnvFile_AppendNewKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=bar\nBAZ=qux\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := MergeEnvFile(path, map[string]string{
		"DIBBLA_API_TOKEN": "ak_1",
	})
	if err != nil {
		t.Fatalf("MergeEnvFile: %v", err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	// Existing keys preserved
	if !strings.Contains(got, "FOO=bar\n") {
		t.Errorf("FOO line missing, got:\n%s", got)
	}
	if !strings.Contains(got, "BAZ=qux\n") {
		t.Errorf("BAZ line missing, got:\n%s", got)
	}
	// New key appended
	if !strings.Contains(got, "DIBBLA_API_TOKEN=ak_1\n") {
		t.Errorf("token line missing, got:\n%s", got)
	}
	// New key must appear AFTER existing keys
	if strings.Index(got, "DIBBLA_API_TOKEN=") < strings.Index(got, "BAZ=") {
		t.Errorf("new key should be appended after existing keys, got:\n%s", got)
	}
}

func TestMergeEnvFile_ReplaceExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=bar\nDIBBLA_API_TOKEN=old_token\nBAZ=qux\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := MergeEnvFile(path, map[string]string{
		"DIBBLA_API_TOKEN": "new_token",
	})
	if err != nil {
		t.Fatalf("MergeEnvFile: %v", err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "old_token") {
		t.Errorf("old token not replaced, got:\n%s", got)
	}
	if !strings.Contains(got, "DIBBLA_API_TOKEN=new_token\n") {
		t.Errorf("new token missing, got:\n%s", got)
	}
	// Line position preserved (between FOO and BAZ)
	fooIdx := strings.Index(got, "FOO=")
	tokenIdx := strings.Index(got, "DIBBLA_API_TOKEN=")
	bazIdx := strings.Index(got, "BAZ=")
	if !(fooIdx < tokenIdx && tokenIdx < bazIdx) {
		t.Errorf("replacement did not preserve position (FOO=%d TOKEN=%d BAZ=%d):\n%s", fooIdx, tokenIdx, bazIdx, got)
	}
	// No duplicates
	if strings.Count(got, "DIBBLA_API_TOKEN=") != 1 {
		t.Errorf("expected exactly one token line, got:\n%s", got)
	}
}

func TestMergeEnvFile_PreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# top comment\nFOO=bar\n\n# section\nBAZ=qux\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := MergeEnvFile(path, map[string]string{"NEW=": "ignored"})
	// Note: "NEW=" is not a valid key (has =) — it would be parsed as literal
	// key "NEW=" which fails isEnvKey. Use a real key instead.
	_ = err

	_, err = MergeEnvFile(path, map[string]string{"NEW_KEY": "v1"})
	if err != nil {
		t.Fatalf("MergeEnvFile: %v", err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "# top comment\n") {
		t.Errorf("top comment lost, got:\n%s", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Errorf("blank line lost, got:\n%s", got)
	}
	if !strings.Contains(got, "# section\n") {
		t.Errorf("section comment lost, got:\n%s", got)
	}
	if !strings.Contains(got, "NEW_KEY=v1\n") {
		t.Errorf("new key missing, got:\n%s", got)
	}
}

func TestMergeEnvFile_PreservesOrdering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "A=1\nB=2\nC=3\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := MergeEnvFile(path, map[string]string{"B": "2_updated"})
	if err != nil {
		t.Fatalf("MergeEnvFile: %v", err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	want := "A=1\nB=2_updated\nC=3\n"
	if got != want {
		t.Errorf("ordering not preserved\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestMergeEnvFile_IdempotentRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	updates := map[string]string{
		"DIBBLA_API_TOKEN": "ak_xyz",
		"DIBBLA_API_URL":   "https://api.dibbla.net",
	}
	if _, err := MergeEnvFile(path, updates); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)

	if _, err := MergeEnvFile(path, updates); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)

	if string(first) != string(second) {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeEnvFile_NoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	_, err := MergeEnvFile(path, map[string]string{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestMergeEnvFile_Perms0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix perms not applicable on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if _, err := MergeEnvFile(path, map[string]string{"K": "v"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("perms: got %o, want 0600", perm)
	}
}

func TestMergeEnvFile_QuotesWhenNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	_, err := MergeEnvFile(path, map[string]string{
		"PLAIN":     "simple_value",
		"WITH_HASH": "has#hash",
		"WITH_QUOT": `has"quote`,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "PLAIN=simple_value\n") {
		t.Errorf("plain value should not be quoted, got:\n%s", got)
	}
	if !strings.Contains(got, `WITH_HASH="has#hash"`) {
		t.Errorf("hash value should be quoted, got:\n%s", got)
	}
	if !strings.Contains(got, `WITH_QUOT="has\"quote"`) {
		t.Errorf("quote should be escaped, got:\n%s", got)
	}
}

func TestEnsureGitignoreEntry_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	modified, err := EnsureGitignoreEntry(path)
	if err != nil {
		t.Fatalf("EnsureGitignoreEntry: %v", err)
	}
	if !modified {
		t.Errorf("expected modified=true for new file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != ".env\n" {
		t.Errorf("new file content: got %q, want %q", data, ".env\n")
	}
}

func TestEnsureGitignoreEntry_AlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	content := "node_modules\n.env\nvendor\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	modified, err := EnsureGitignoreEntry(path)
	if err != nil {
		t.Fatalf("EnsureGitignoreEntry: %v", err)
	}
	if modified {
		t.Errorf("expected modified=false when .env already present")
	}
	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Errorf("file modified unexpectedly:\nwant:\n%s\ngot:\n%s", content, data)
	}
}

func TestEnsureGitignoreEntry_SlashDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	content := "node_modules\n/.env\nvendor\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	modified, err := EnsureGitignoreEntry(path)
	if err != nil {
		t.Fatalf("EnsureGitignoreEntry: %v", err)
	}
	if modified {
		t.Errorf("expected modified=false when /.env already present")
	}
}

func TestEnsureGitignoreEntry_AppendWithMissingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules"), 0644); err != nil {
		t.Fatal(err)
	}

	modified, err := EnsureGitignoreEntry(path)
	if err != nil {
		t.Fatalf("EnsureGitignoreEntry: %v", err)
	}
	if !modified {
		t.Errorf("expected modified=true")
	}
	data, _ := os.ReadFile(path)
	want := "node_modules\n.env\n"
	if string(data) != want {
		t.Errorf("append with missing newline:\nwant:\n%s\ngot:\n%s", want, data)
	}
}

func TestEnsureGitignoreEntry_AppendToExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	content := "node_modules\nvendor\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	modified, err := EnsureGitignoreEntry(path)
	if err != nil {
		t.Fatalf("EnsureGitignoreEntry: %v", err)
	}
	if !modified {
		t.Errorf("expected modified=true")
	}
	data, _ := os.ReadFile(path)
	want := "node_modules\nvendor\n.env\n"
	if string(data) != want {
		t.Errorf("append:\nwant:\n%s\ngot:\n%s", want, data)
	}
}
