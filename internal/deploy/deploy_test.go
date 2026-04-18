package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateArchive_POSIXPaths(t *testing.T) {
	// Create a temp directory with nested structure mimicking a webapp
	dir := t.TempDir()

	// Create frontend/package.json (the exact case from the bug report)
	frontendDir := filepath.Join(dir, "frontend")
	if err := os.Mkdir(frontendDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "package-lock.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a deeper nested path
	srcDir := filepath.Join(dir, "src", "components")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "App.js"), []byte(`console.log("hi")`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create Dockerfile at root
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(`FROM node:18`), 0644); err != nil {
		t.Fatal(err)
	}

	// Build archive
	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive failed: %v", err)
	}

	// Read back all tar entry names
	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}

	// Verify all paths use forward slashes (POSIX)
	for _, name := range names {
		if strings.Contains(name, `\`) {
			t.Errorf("tar entry contains backslash: %q", name)
		}
	}

	// Verify expected entries are present
	expected := map[string]bool{
		"frontend/":                  true,
		"frontend/package.json":      true,
		"frontend/package-lock.json": true,
		"src/":                       true,
		"src/components/":            true,
		"src/components/App.js":      true,
		"Dockerfile":                 true,
	}
	found := make(map[string]bool)
	for _, name := range names {
		found[name] = true
	}
	for exp := range expected {
		if !found[exp] {
			t.Errorf("expected tar entry %q not found; got entries: %v", exp, names)
		}
	}
}

func TestCreateArchive_ExcludesCorrectPaths(t *testing.T) {
	dir := t.TempDir()

	// Create files that should be included
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files/dirs that should be excluded
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(`ref: refs/heads/main`), 0644); err != nil {
		t.Fatal(err)
	}

	nmDir := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(nmDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, "foo.js"), []byte(`module.exports={}`), 0644); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive failed: %v", err)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(hdr.Name, ".git") || strings.HasPrefix(hdr.Name, "node_modules") {
			t.Errorf("excluded path found in archive: %q", hdr.Name)
		}
	}
}

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
		isDir   bool
		want    bool
	}{
		{"git dir", ".git", true, true},
		{"node_modules dir", "node_modules", true, true},
		{"nested in git", ".git/HEAD", false, true},
		{"env prod", ".env.production", false, true},
		{"pem file", "certs/server.pem", false, true},
		{"key file", "secrets/private.key", false, true},
		{"normal file", "main.go", false, false},
		{"nested normal", "src/app.js", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := fakeFileInfo{name: filepath.Base(tt.relPath), dir: tt.isDir}
			got := shouldExclude(tt.relPath, info)
			if got != tt.want {
				t.Errorf("shouldExclude(%q) = %v, want %v", tt.relPath, got, tt.want)
			}
		})
	}
}

// fakeFileInfo implements os.FileInfo for testing shouldExclude
type fakeFileInfo struct {
	name string
	dir  bool
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0644 }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() interface{}   { return nil }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }

func TestFormatAPIError_WithLogs(t *testing.T) {
	resp := &ErrorResponse{
		Status: "error",
		Error: ErrorDetail{
			Code:    "BUILD_FAILED",
			Message: "Image build failed",
			Logs:    "Step 3/8 : COPY frontend/package.json ./\n ---> ERROR: not found",
		},
	}

	err := formatAPIError(resp)
	errMsg := err.Error()

	if !strings.Contains(errMsg, "BUILD_FAILED") {
		t.Errorf("error should contain code, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "Build logs:") {
		t.Errorf("error should contain build logs section, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "frontend/package.json") {
		t.Errorf("error should contain the actual log content, got: %s", errMsg)
	}
}

func TestFormatAPIError_WithoutLogs(t *testing.T) {
	resp := &ErrorResponse{
		Status: "error",
		Error: ErrorDetail{
			Code:    "VALIDATION_ERROR",
			Message: "Invalid app name",
		},
	}

	err := formatAPIError(resp)
	errMsg := err.Error()

	if strings.Contains(errMsg, "Build logs:") {
		t.Errorf("error should not contain build logs section when no logs, got: %s", errMsg)
	}
}

// --- symlink handling tests --------------------------------------------------

type tarEntry struct {
	typeflag byte
	content  []byte
	linkname string
}

func readTarEntries(t *testing.T, archiveBytes []byte) map[string]tarEntry {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	out := make(map[string]tarEntry)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var content []byte
		if hdr.Typeflag == tar.TypeReg {
			b, rerr := io.ReadAll(tr)
			if rerr != nil {
				t.Fatal(rerr)
			}
			content = b
		}
		out[hdr.Name] = tarEntry{typeflag: hdr.Typeflag, content: content, linkname: hdr.Linkname}
	}
	return out
}

func entryNames(m map[string]tarEntry) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return names
}

func TestCreateArchive_Symlink_FileWithinRoot_Dereferenced(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0755); err != nil {
		t.Fatal(err)
	}
	realContent := []byte("the real content")
	if err := os.WriteFile(filepath.Join(dir, "b", "real.txt"), realContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	// a/link.txt -> ../b/real.txt (relative, target inside dir)
	if err := os.Symlink(filepath.Join("..", "b", "real.txt"), filepath.Join(dir, "a", "link.txt")); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}
	entries := readTarEntries(t, archiveBytes)

	got, ok := entries["a/link.txt"]
	if !ok {
		t.Fatalf("expected entry a/link.txt in archive, got: %v", entryNames(entries))
	}
	if got.typeflag != tar.TypeReg {
		t.Errorf("a/link.txt: want TypeReg, got %v", got.typeflag)
	}
	if string(got.content) != string(realContent) {
		t.Errorf("a/link.txt content: want %q, got %q", realContent, got.content)
	}
	for name, e := range entries {
		if e.typeflag == tar.TypeSymlink {
			t.Errorf("unexpected symlink entry %q (linkname=%q)", name, e.linkname)
		}
	}
}

func TestCreateArchive_Symlink_DirWithinRoot_Dereferenced(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real-skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real-skills", "file1.md"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real-skills", "file2.md"), []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	// a/skills -> ../real-skills (directory symlink, like .claude/skills/dibbla -> ../../.agents/skills/dibbla)
	if err := os.Symlink(filepath.Join("..", "real-skills"), filepath.Join(dir, "a", "skills")); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}
	entries := readTarEntries(t, archiveBytes)

	wantFiles := map[string]string{
		"a/skills/file1.md": "one",
		"a/skills/file2.md": "two",
	}
	for name, wantContent := range wantFiles {
		e, ok := entries[name]
		if !ok {
			t.Errorf("missing expected entry %q, got: %v", name, entryNames(entries))
			continue
		}
		if e.typeflag != tar.TypeReg {
			t.Errorf("%s: want TypeReg, got %v", name, e.typeflag)
		}
		if string(e.content) != wantContent {
			t.Errorf("%s content: want %q, got %q", name, wantContent, e.content)
		}
	}
	if _, ok := entries["a/skills/"]; !ok {
		t.Errorf("missing dir entry a/skills/, got: %v", entryNames(entries))
	}
	for name, e := range entries {
		if e.typeflag == tar.TypeSymlink {
			t.Errorf("unexpected symlink %q", name)
		}
		if strings.Contains(name, "..") {
			t.Errorf("entry name %q contains '..'", name)
		}
	}
}

func TestCreateArchive_Symlink_EscapingRoot_Skipped(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("HOST SECRET"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "regular.txt"), []byte("kept"), 0644); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}
	entries := readTarEntries(t, archiveBytes)

	if _, ok := entries["link"]; ok {
		t.Errorf("escaping symlink 'link' should be skipped, got content: %q", entries["link"].content)
	}
	if _, ok := entries["regular.txt"]; !ok {
		t.Errorf("regular.txt should still be present, got: %v", entryNames(entries))
	}
	for name, e := range entries {
		if strings.Contains(string(e.content), "HOST SECRET") {
			t.Errorf("host secret leaked into entry %q", name)
		}
	}
}

func TestCreateArchive_Symlink_Absolute_Skipped(t *testing.T) {
	dir := t.TempDir()
	target := "/etc/hosts"
	if _, err := os.Stat(target); err != nil {
		t.Skipf("required target %s not available: %v", target, err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "secrets")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}
	entries := readTarEntries(t, archiveBytes)

	if _, ok := entries["secrets"]; ok {
		t.Errorf("absolute symlink should be skipped, got entry (content=%q)", entries["secrets"].content)
	}
	if _, ok := entries["ok.txt"]; !ok {
		t.Errorf("regular file should still be present, got: %v", entryNames(entries))
	}
}

func TestCreateArchive_Symlink_Cycle_Terminates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	// sub/loop -> ../sub  (in-root, but creates a visible self-loop under dereference)
	if err := os.Symlink(filepath.Join("..", "sub"), filepath.Join(dir, "sub", "loop")); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var (
		archiveBytes []byte
		archiveErr   error
	)
	go func() {
		archiveBytes, archiveErr = createArchive(dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("createArchive did not terminate within 5s — cycle detection failed")
	}

	if archiveErr != nil {
		t.Fatalf("createArchive: %v", archiveErr)
	}
	entries := readTarEntries(t, archiveBytes)
	if _, ok := entries["sub/a.txt"]; !ok {
		t.Errorf("expected sub/a.txt, got: %v", entryNames(entries))
	}
}

func TestCreateArchive_Symlink_Broken_Skipped(t *testing.T) {
	dir := t.TempDir()
	// dangling -> <dir>/nonexistent (target inside dir but doesn't exist)
	if err := os.Symlink(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	archiveBytes, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}
	entries := readTarEntries(t, archiveBytes)

	if _, ok := entries["dangling"]; ok {
		t.Errorf("broken symlink should be skipped, got entry")
	}
	if _, ok := entries["ok.txt"]; !ok {
		t.Errorf("regular file should be present, got: %v", entryNames(entries))
	}
}
