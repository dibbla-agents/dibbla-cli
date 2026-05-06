package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateArchive_SubstitutesShellVarsInRootManifest end-to-end-ish:
// drops a dibbla.yaml with `${VAR}` placeholders + a Dockerfile + a nested
// file with the same `${VAR}` pattern into a tempdir, runs createArchive
// with a controlled shell env, and inspects the resulting tar to confirm
// only the ROOT manifest was substituted; everything else is byte-identical.
func TestCreateArchive_SubstitutesShellVarsInRootManifest(t *testing.T) {
	dir := t.TempDir()
	rootManifest := []byte(`version: 1
services:
  web:
    build: .
    port: 3000
    public: true
    environment:
      APP_VERSION: ${BUILD_VERSION:-dev}
      USER_HOME: ${HOME}
      REDIS_URL: ${DIBBLA_SVC_REDIS_URL}
`)
	if err := os.WriteFile(filepath.Join(dir, "dibbla.yaml"), rootManifest, 0o644); err != nil {
		t.Fatalf("write root yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	// Nested file with the same ${VAR} pattern — must be left alone.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := []byte("# this dibbla.yaml is in a subdir; do NOT substitute\nx: ${HOME}\n")
	if err := os.WriteFile(filepath.Join(dir, "sub", "dibbla.yaml"), nested, 0o644); err != nil {
		t.Fatalf("write nested yaml: %v", err)
	}

	t.Setenv("BUILD_VERSION", "v9.9.9")
	t.Setenv("HOME", "/home/erik")

	archive, err := createArchive(dir)
	if err != nil {
		t.Fatalf("createArchive: %v", err)
	}

	files := readTarGz(t, archive)
	root, has := files["dibbla.yaml"]
	if !has {
		t.Fatalf("root dibbla.yaml missing from archive")
	}
	gotRoot := string(root)
	if !strings.Contains(gotRoot, "APP_VERSION: v9.9.9") {
		t.Errorf("BUILD_VERSION not substituted in root manifest: %s", gotRoot)
	}
	if !strings.Contains(gotRoot, "USER_HOME: /home/erik") {
		t.Errorf("HOME not substituted in root manifest: %s", gotRoot)
	}
	if !strings.Contains(gotRoot, "REDIS_URL: ${DIBBLA_SVC_REDIS_URL}") {
		t.Errorf("DIBBLA_SVC_REDIS_URL must pass through (server resolves): %s", gotRoot)
	}

	subContent, has := files["sub/dibbla.yaml"]
	if !has {
		t.Fatalf("nested dibbla.yaml missing from archive")
	}
	if string(subContent) != string(nested) {
		t.Errorf("nested dibbla.yaml must NOT be substituted; got=%q want=%q", string(subContent), string(nested))
	}

	if string(files["Dockerfile"]) != "FROM scratch\n" {
		t.Errorf("Dockerfile must pass through unchanged: %q", string(files["Dockerfile"]))
	}
}

// TestRun_FailsBeforeUploadOnUnresolvedVar — when a shell var has no value
// and no default, `dibbla deploy` must fail BEFORE the upload, not silently
// ship an empty value to the pod.
func TestRun_FailsBeforeUploadOnUnresolvedVar(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`version: 1
services:
  app:
    build: .
    port: 3000
    public: true
    environment:
      MISSING: ${TOTALLY_UNSET_VAR}
`)
	_ = os.WriteFile(filepath.Join(dir, "dibbla.yaml"), yaml, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)

	// Make sure the var really isn't set in the test env.
	os.Unsetenv("TOTALLY_UNSET_VAR")

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	_, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Path: dir, Alias: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for unresolved shell var")
	}
	if !strings.Contains(err.Error(), "TOTALLY_UNSET_VAR") {
		t.Errorf("error should name the unresolved var: %v", err)
	}
	if hits != 0 {
		t.Errorf("server must NOT be called when shell-var substitution fails; got %d hits", hits)
	}
}

// TestRun_PassesShellVarsThroughToUpload — happy path with everything set:
// the server receives the substituted YAML, not the placeholders.
func TestRun_PassesShellVarsThroughToUpload(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`version: 1
services:
  app:
    build: .
    port: 3000
    public: true
    environment:
      APP_VERSION: ${BUILD_VERSION}
`)
	_ = os.WriteFile(filepath.Join(dir, "dibbla.yaml"), yaml, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	t.Setenv("BUILD_VERSION", "v1.2.3")

	var sawArchive []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(50 << 20)
		fhs := r.MultipartForm.File["archive"]
		if len(fhs) > 0 {
			f, _ := fhs[0].Open()
			defer f.Close()
			b, _ := io.ReadAll(f)
			sawArchive = b
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"success","deployment":{"id":"x","alias":"x","url":"x","status":"running","created_at":"2024-01-01T00:00:00Z","deployed_at":"2024-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	_, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Path: dir, Alias: "x"}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	files := readTarGz(t, sawArchive)
	got := string(files["dibbla.yaml"])
	if !strings.Contains(got, "APP_VERSION: v1.2.3") {
		t.Errorf("server should have received substituted YAML: %s", got)
	}
	if strings.Contains(got, "${BUILD_VERSION}") {
		t.Errorf("placeholder must be gone from uploaded YAML: %s", got)
	}
}

// readTarGz unpacks an in-memory tar.gz into name → bytes for inspection.
func readTarGz(t *testing.T, blob []byte) map[string][]byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}
		out[hdr.Name] = b
	}
	return out
}
