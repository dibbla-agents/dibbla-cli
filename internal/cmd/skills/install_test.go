package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func sampleBlock() string {
	return renderPointerBlock("dibbla", "v1.2.3")
}

func TestRenderAgentsBlock_InsertsIntoEmpty(t *testing.T) {
	out := renderAgentsBlock("", sampleBlock())
	if !strings.HasPrefix(out, markerOpen) {
		t.Errorf("expected block at start, got %q", out[:min(len(out), 40)])
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline, got %q", out[max(0, len(out)-20):])
	}
	if strings.Count(out, markerOpen) != 1 || strings.Count(out, markerClose) != 1 {
		t.Errorf("expected exactly one marker pair, got %d open / %d close",
			strings.Count(out, markerOpen), strings.Count(out, markerClose))
	}
}

func TestRenderAgentsBlock_InsertsIntoWhitespaceOnly(t *testing.T) {
	out := renderAgentsBlock("\n\n\n", sampleBlock())
	if !strings.HasPrefix(out, markerOpen) {
		t.Errorf("whitespace-only input should be treated as empty; got %q", out[:min(len(out), 40)])
	}
}

func TestRenderAgentsBlock_AppendsAfterExistingContent(t *testing.T) {
	existing := "# My Project\n\nSome notes here.\n"
	out := renderAgentsBlock(existing, sampleBlock())
	if !strings.HasPrefix(out, "# My Project") {
		t.Errorf("existing content should remain at top; got %q", out[:40])
	}
	if !strings.Contains(out, markerOpen) {
		t.Error("expected block to be appended")
	}
	// Ensure blank line separator between existing content and block.
	if !strings.Contains(out, "Some notes here.\n\n"+markerOpen) {
		t.Errorf("expected blank line separator before block; got:\n%s", out)
	}
}

func TestRenderAgentsBlock_ReplacesExistingBlock(t *testing.T) {
	oldBlock := renderPointerBlock("dibbla", "v1.0.0")
	existing := "Header above\n\n" + oldBlock + "\n\nFooter below.\n"
	out := renderAgentsBlock(existing, sampleBlock())
	if strings.Contains(out, "v1.0.0") {
		t.Error("old version string should be gone after replace")
	}
	if !strings.Contains(out, "v1.2.3") {
		t.Error("new version string should be present")
	}
	if !strings.Contains(out, "Header above") {
		t.Error("content before block must be preserved")
	}
	if !strings.Contains(out, "Footer below.") {
		t.Error("content after block must be preserved")
	}
	if strings.Count(out, markerOpen) != 1 {
		t.Errorf("expected exactly one open marker after replace, got %d", strings.Count(out, markerOpen))
	}
}

func TestRenderAgentsBlock_Idempotent(t *testing.T) {
	once := renderAgentsBlock("", sampleBlock())
	twice := renderAgentsBlock(once, sampleBlock())
	if once != twice {
		t.Errorf("expected idempotent result.\nonce:\n%s\ntwice:\n%s", once, twice)
	}
}

func TestRenderAgentsBlock_PreservesCRLF(t *testing.T) {
	existing := "# Project\r\n\r\nCRLF content here.\r\n"
	out := renderAgentsBlock(existing, sampleBlock())
	if strings.Contains(out, "\r\n") == false {
		t.Error("CRLF input should yield CRLF output")
	}
	// No bare \n (not preceded by \r) in the block region.
	stripped := strings.ReplaceAll(out, "\r\n", "")
	if strings.Contains(stripped, "\n") {
		t.Errorf("CRLF output should not contain bare LF; got:\n%q", out)
	}
}

func TestRenderAgentsBlock_PreservesLF(t *testing.T) {
	existing := "# Project\n\nLF content.\n"
	out := renderAgentsBlock(existing, sampleBlock())
	if strings.Contains(out, "\r\n") {
		t.Errorf("LF input should not gain CRLF endings; got:\n%q", out)
	}
}

func TestRenderAgentsBlock_HalfMarkerAppends(t *testing.T) {
	existing := "User text\n\n" + markerOpen + "\n\nuser content that happens to contain an opener but no closer\n"
	out := renderAgentsBlock(existing, sampleBlock())
	if !strings.Contains(out, "user content that happens to contain an opener") {
		t.Error("half-marker input should treat as append, preserving user content")
	}
	if strings.Count(out, markerOpen) != 2 {
		t.Errorf("expected original stray opener plus new block opener (2 total), got %d", strings.Count(out, markerOpen))
	}
}

func TestRenderAgentsBlock_TrailingNewlineNormalized(t *testing.T) {
	cases := []string{"# X", "# X\n", "# X\n\n\n"}
	for _, existing := range cases {
		out := renderAgentsBlock(existing, sampleBlock())
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("input %q: output should end with exactly one newline", existing)
		}
		if strings.HasSuffix(out, "\n\n") {
			t.Errorf("input %q: output should not have multiple trailing newlines", existing)
		}
	}
}

func TestWriteAgentsFile_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeAgentsFile(path, sampleBlock()); err != nil {
		t.Fatalf("writeAgentsFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), markerOpen) {
		t.Error("new AGENTS.md should contain the block")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("expected 0644, got %o", info.Mode().Perm())
	}
}

func TestWriteAgentsFile_NoOpWhenIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeAgentsFile(path, sampleBlock()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	if err := writeAgentsFile(path, sampleBlock()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("second write should be a no-op; mtime changed %v → %v", before.ModTime(), after.ModTime())
	}
}

func TestWriteAgentsFile_PreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentsFile(path, sampleBlock()); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected mode preserved at 0600, got %o", info.Mode().Perm())
	}
}

func TestWriteAgentsFile_NoTempLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeAgentsFile(path, sampleBlock()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".dibbla.tmp") {
			t.Errorf("found leftover temp file: %s", e.Name())
		}
	}
}

func TestWriteSkillFiles_CreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".claude", "skills", "dibbla")

	entry, err := find("dibbla")
	if err != nil {
		t.Fatal(err)
	}
	skillFS, err := entry.files()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSkillFiles(skillFS, destDir, dir, false); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"SKILL.md", "examples.md", "guardrails.md", "reference.md"} {
		if _, err := os.Stat(filepath.Join(destDir, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
}

func TestWriteSkillFiles_IdempotentNoOp(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".claude", "skills", "dibbla")
	entry, _ := find("dibbla")
	skillFS, _ := entry.files()

	if err := writeSkillFiles(skillFS, destDir, dir, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(destDir, "SKILL.md"))
	time.Sleep(10 * time.Millisecond)
	if err := writeSkillFiles(skillFS, destDir, dir, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(destDir, "SKILL.md"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("rerun should be no-op; SKILL.md mtime changed")
	}
}

func TestWriteSkillFiles_RequiresForceOnConflict(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".claude", "skills", "dibbla")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "SKILL.md"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, _ := find("dibbla")
	skillFS, _ := entry.files()

	err := writeSkillFiles(skillFS, destDir, dir, false)
	if err == nil {
		t.Fatal("expected error without --force, got nil")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should mention --force; got: %v", err)
	}
}

func TestWriteSkillFiles_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".claude", "skills", "dibbla")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "SKILL.md"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, _ := find("dibbla")
	skillFS, _ := entry.files()

	if err := writeSkillFiles(skillFS, destDir, dir, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if string(data) == "hand-edited\n" {
		t.Error("--force should have overwritten the file")
	}
}

func TestWriteSkillFiles_LeavesUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".claude", "skills", "dibbla")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(destDir, "custom.md")
	if err := os.WriteFile(userFile, []byte("user content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, _ := find("dibbla")
	skillFS, _ := entry.files()

	if err := writeSkillFiles(skillFS, destDir, dir, true); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("user file was removed: %v", err)
	}
	if string(data) != "user content\n" {
		t.Errorf("user file was modified: %q", string(data))
	}
}

func TestWriteSkillFiles_WithFakeFS(t *testing.T) {
	// Guard that walking works with any fs.FS, not just our embed.
	fake := fstest.MapFS{
		"SKILL.md":    {Data: []byte("hello\n")},
		"sub/nest.md": {Data: []byte("nested\n")},
	}
	dir := t.TempDir()
	destDir := filepath.Join(dir, "out")
	if err := writeSkillFiles(fake, destDir, dir, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"SKILL.md", "sub/nest.md"} {
		if _, err := os.Stat(filepath.Join(destDir, rel)); err != nil {
			t.Errorf("%s not written: %v", rel, err)
		}
	}
}

func TestResolveTargetRoot_ProjectDefault(t *testing.T) {
	got, err := resolveTargetRoot(false)
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if got != wd {
		t.Errorf("got %q, want cwd %q", got, wd)
	}
}

func TestResolveTargetRoot_UserFlag(t *testing.T) {
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	got, err := resolveTargetRoot(true)
	if err != nil {
		t.Fatal(err)
	}
	// macOS UserHomeDir may resolve symlinks; check either matches.
	if got != fake {
		resolved, _ := filepath.EvalSymlinks(fake)
		if got != resolved {
			t.Errorf("got %q, want %q (or resolved %q)", got, fake, resolved)
		}
	}
}

func TestValidateSkillID_AcceptsValid(t *testing.T) {
	valid := []string{"dibbla", "foo-bar", "x", "a1", "multi-dash-name"}
	for _, id := range valid {
		if err := validateSkillID(id); err != nil {
			t.Errorf("validateSkillID(%q) unexpectedly failed: %v", id, err)
		}
	}
}

func TestValidateSkillID_RejectsInvalid(t *testing.T) {
	invalid := []string{"", "/etc/passwd", "..", "Foo", "1abc", "has space", "has/slash", "with\\bs"}
	for _, id := range invalid {
		if err := validateSkillID(id); err == nil {
			t.Errorf("validateSkillID(%q) should have failed", id)
		}
	}
}

// min/max helpers — go <1.21 compat isn't needed here since we target modern go,
// but keep them local so this file doesn't depend on build-tagged helpers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Sanity guard: make sure the sub-FS returned by entry.files() walks properly.
func TestEntryFiles_Walkable(t *testing.T) {
	entry, _ := find("dibbla")
	skillFS, err := entry.files()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	_ = fs.WalkDir(skillFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if count < 4 {
		t.Errorf("expected at least 4 embedded files, got %d", count)
	}
}
