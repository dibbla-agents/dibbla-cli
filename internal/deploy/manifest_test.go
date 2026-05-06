package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// fakeDeployServer accepts a single multipart upload and records what it
// received so tests can assert the wire shape.
type fakeDeployServer struct {
	srv      *httptest.Server
	called   int32
	formVals map[string]string
	hasFile  bool
}

func newFakeDeployServer(t *testing.T) *fakeDeployServer {
	t.Helper()
	f := &fakeDeployServer{formVals: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.called, 1)
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				f.formVals[k] = v[0]
			}
		}
		if r.MultipartForm.File["archive"] != nil {
			f.hasFile = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status":"success","deployment":{"id":"dep_x","alias":"shop","url":"https://shop.dibbla.com","status":"running","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","require_login":false}}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestRunFailsOnInvalidManifestBeforeUpload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	writeFile(t, dir, "dibbla.yaml", "version: 99\nservices:\n  app: { build: . }\n")

	f := newFakeDeployServer(t)
	_, err := Run(Options{
		APIURL:   f.srv.URL,
		APIToken: "tok",
		Path:     dir,
	}, nil)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Errorf("expected manifest validation message, got: %v", err)
	}
	if atomic.LoadInt32(&f.called) != 0 {
		t.Errorf("server should NOT have been called for invalid manifest")
	}
}

func TestRunFailsWhenBothManifestFilesPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	writeFile(t, dir, "dibbla.yaml", "version: 1\nservices:\n  app: { build: . }\n")
	writeFile(t, dir, "dibbla.yml", "version: 1\nservices:\n  app: { build: . }\n")

	f := newFakeDeployServer(t)
	_, err := Run(Options{
		APIURL:   f.srv.URL,
		APIToken: "tok",
		Path:     dir,
	}, nil)
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("expected 'both' in error, got: %v", err)
	}
	if atomic.LoadInt32(&f.called) != 0 {
		t.Errorf("server should NOT have been called when manifest is ambiguous")
	}
}

func TestRunValidManifestForwardsTargetEnvAndProfiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: .
    port: 3000
    public: true
`)
	f := newFakeDeployServer(t)
	_, err := Run(Options{
		APIURL:    f.srv.URL,
		APIToken:  "tok",
		Path:      dir,
		Alias:     "shop",
		TargetEnv: "staging",
		Profiles:  []string{"canary"},
		NoPublic:  false,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := f.formVals["env"]; got != "staging" {
		t.Errorf("env field: want staging, got %q", got)
	}
	if got := f.formVals["profiles"]; got != `["canary"]` {
		t.Errorf("profiles field: want JSON array, got %q", got)
	}
	if _, ok := f.formVals["no_public"]; ok {
		t.Errorf("no_public should be omitted when false")
	}
}

func TestRunNoPublicFlagSerialized(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	f := newFakeDeployServer(t)
	_, err := Run(Options{
		APIURL:   f.srv.URL,
		APIToken: "tok",
		Path:     dir,
		Alias:    "shop",
		NoPublic: true,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := f.formVals["no_public"]; got != "true" {
		t.Errorf("no_public field: want true, got %q", got)
	}
}

// TestRunNoManifestPreservesLegacyMultipart asserts the byte-stable
// backcompat invariant: a legacy project (no dibbla.yaml, no new flags) still
// produces a multipart form with no `env`, `profiles`, or `no_public` fields.
// The set of form keys observed by the server must be exactly the legacy set.
func TestRunNoManifestPreservesLegacyMultipart(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	f := newFakeDeployServer(t)
	_, err := Run(Options{
		APIURL:   f.srv.URL,
		APIToken: "tok",
		Path:     dir,
		Alias:    "shop",
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, newField := range []string{"env", "profiles", "no_public"} {
		if _, ok := f.formVals[newField]; ok {
			t.Errorf("legacy upload should not include %s field; got %q", newField, f.formVals[newField])
		}
	}
	if got := f.formVals["app_name"]; got != "shop" {
		t.Errorf("app_name: want shop, got %q", got)
	}
	if !f.hasFile {
		t.Errorf("archive must be uploaded")
	}
}

func TestValidateLocalManifestNoOpWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	if err := validateLocalManifest(dir); err != nil {
		t.Errorf("absent manifest should be no-op, got %v", err)
	}
}

func TestValidateLocalManifestPassesValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    build: .
    port: 3000
    public: true
`)
	if err := validateLocalManifest(dir); err != nil {
		t.Errorf("valid manifest should pass, got %v", err)
	}
}
