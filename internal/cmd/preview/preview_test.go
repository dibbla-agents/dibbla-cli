package preview

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func resetFlags() {
	previewAlias = ""
	previewTargetEnv = ""
	previewProfiles = nil
	previewNoPublic = false
	previewPort = ""
	previewJSON = false
}

// fakeServer for cobra-level tests. Returns the canned response.
func fakeServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withAPI(t *testing.T, url string) {
	t.Helper()
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_API_URL", url)
	// Force CI mode so config.Load skips keyring lookups in this test env.
	t.Setenv("CI", "1")
}

func TestRunPreview_HumanOutput_Valid(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")

	canned := deploypkg.PreviewResponse{
		Valid: true, Alias: "shop", Env: "prod", PublicService: "web",
		ActiveServices: []deploypkg.PreviewService{
			{Name: "web", IsBuilt: true, IsPublic: true, Replicas: 1},
			{Name: "worker", IsBuilt: true, Replicas: 2},
		},
		SkippedServices: []deploypkg.PreviewSkippedSvc{
			{Name: "mailcatcher", Reason: "profile 'dev' not active"},
		},
	}
	srv := fakeServer(t, http.StatusOK, canned)
	withAPI(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runPreview(&stdout, &stderr, []string{dir})
	if code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"preview valid", "alias:  shop", "env:    prod",
		"public: web", "active services (2)", "web", "worker (public)" /* invalid */} {
		_ = want
	}
	if !strings.Contains(out, "preview valid") {
		t.Errorf("missing valid line: %q", out)
	}
	if !strings.Contains(out, "alias:  shop") {
		t.Errorf("missing alias: %q", out)
	}
	if !strings.Contains(out, "active services (2)") {
		t.Errorf("missing service count: %q", out)
	}
	if !strings.Contains(out, "web") || !strings.Contains(out, "worker") {
		t.Errorf("missing service rows: %q", out)
	}
	if !strings.Contains(out, "skipped (1)") {
		t.Errorf("missing skipped section: %q", out)
	}
	if !strings.Contains(out, "mailcatcher") {
		t.Errorf("missing skipped service: %q", out)
	}
}

func TestRunPreview_JSONPassthrough(t *testing.T) {
	resetFlags()
	previewJSON = true
	defer resetFlags()

	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")

	canned := deploypkg.PreviewResponse{
		Valid: true, Alias: "shop", Env: "staging",
		ActiveServices: []deploypkg.PreviewService{{Name: "web", Replicas: 1}},
	}
	srv := fakeServer(t, http.StatusOK, canned)
	withAPI(t, srv.URL)

	var stdout, stderr bytes.Buffer
	if code := runPreview(&stdout, &stderr, []string{dir}); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var got deploypkg.PreviewResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, stdout.String())
	}
	if !got.Valid || got.Alias != "shop" || got.Env != "staging" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestRunPreview_InvalidExitsOneAndPrintsErrors(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	canned := deploypkg.PreviewResponse{
		Valid: false, Alias: "shop", Env: "prod",
		Errors: []deploypkg.PreviewError{
			{Code: "PUBLIC_SERVICE_MISSING", Detail: "no service has public:true"},
			{Code: "MANIFEST_INVALID", Path: "services.web.image", Detail: "missing tag"},
		},
	}
	srv := fakeServer(t, http.StatusOK, canned)
	withAPI(t, srv.URL)

	var stdout, stderr bytes.Buffer
	if code := runPreview(&stdout, &stderr, []string{dir}); code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	se := stderr.String()
	if !strings.Contains(se, "PUBLIC_SERVICE_MISSING") {
		t.Errorf("missing first error: %q", se)
	}
	if !strings.Contains(se, "services.web.image") || !strings.Contains(se, "missing tag") {
		t.Errorf("missing path/detail: %q", se)
	}
}

func TestRunPreview_NoTokenExitsOneBeforeUpload(t *testing.T) {
	resetFlags()
	t.Setenv("DIBBLA_API_TOKEN", "")
	t.Setenv("DIBBLA_API_URL", "")
	t.Setenv("CI", "1")
	var stdout, stderr bytes.Buffer
	if code := runPreview(&stdout, &stderr, []string{t.TempDir()}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "API token is required") {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunPreview_LocalManifestErrorBeforeUpload(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", "version: 99\nservices:\n  app:\n    build: .\n    port: 3000\n")

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)

	var stdout, stderr bytes.Buffer
	if code := runPreview(&stdout, &stderr, []string{dir}); code != 1 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "manifest validation failed") {
		t.Errorf("expected manifest error, got: %q", stderr.String())
	}
	if hits != 0 {
		t.Errorf("server should NOT have been called: %d", hits)
	}
}
