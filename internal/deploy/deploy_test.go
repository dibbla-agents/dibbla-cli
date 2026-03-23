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

func (f fakeFileInfo) Name() string      { return f.name }
func (f fakeFileInfo) Size() int64       { return 0 }
func (f fakeFileInfo) Mode() os.FileMode { return 0644 }
func (f fakeFileInfo) IsDir() bool       { return f.dir }
func (f fakeFileInfo) Sys() interface{}  { return nil }
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
