package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakePreviewServer accepts a multipart preview request and records what it
// received plus the canned response it should return.
type fakePreviewServer struct {
	srv      *httptest.Server
	called   int32
	formVals map[string]string
	hasFile  bool
	response any    // PreviewResponse | ErrorResponse
	status   int
}

func newFakePreviewServer(t *testing.T, status int, response any) *fakePreviewServer {
	t.Helper()
	f := &fakePreviewServer{formVals: map[string]string{}, response: response, status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.called, 1)
		if r.URL.Path != "/api/deploy/deployments/preview" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "missing/wrong auth: "+got, http.StatusUnauthorized)
			return
		}
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
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(f.response)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestPreview_Valid_ForwardsArchiveAndFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: .
    port: 3000
    public: true
`)
	canned := PreviewResponse{
		Valid: true, Alias: "shop", Env: "staging", PublicService: "web",
		ActiveServices: []PreviewService{{Name: "web", IsBuilt: true, IsPublic: true, Replicas: 1}},
	}
	f := newFakePreviewServer(t, http.StatusOK, canned)

	resp, err := Preview(PreviewOptions{
		APIURL:    f.srv.URL,
		APIToken:  "test-token",
		Path:      dir,
		Alias:     "shop",
		TargetEnv: "staging",
		Profiles:  []string{"mailcatcher", "metrics"},
		NoPublic:  true,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !resp.Valid || resp.PublicService != "web" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if got := f.formVals["app_name"]; got != "shop" {
		t.Errorf("app_name: %q", got)
	}
	if got := f.formVals["env"]; got != "staging" {
		t.Errorf("env: %q", got)
	}
	if got := f.formVals["profiles"]; got != `["mailcatcher","metrics"]` {
		t.Errorf("profiles: %q", got)
	}
	if got := f.formVals["no_public"]; got != "true" {
		t.Errorf("no_public: %q", got)
	}
	if !f.hasFile {
		t.Error("archive must be present")
	}
}

func TestPreview_LegacyNoManifest_OmitsManifestFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	canned := PreviewResponse{
		Valid: true, Alias: "shop", Env: "prod", PublicService: "app",
		ActiveServices: []PreviewService{{Name: "app", IsBuilt: true, IsPublic: true, Replicas: 1}},
	}
	f := newFakePreviewServer(t, http.StatusOK, canned)

	resp, err := Preview(PreviewOptions{
		APIURL:   f.srv.URL,
		APIToken: "test-token",
		Path:     dir,
		Alias:    "shop",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !resp.Valid {
		t.Errorf("expected valid: %+v", resp)
	}
	for _, k := range []string{"env", "profiles", "no_public"} {
		if v, ok := f.formVals[k]; ok && v != "" {
			t.Errorf("legacy preview should omit %s, got %q", k, v)
		}
	}
}

func TestPreview_ServerReportsValidationErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	canned := PreviewResponse{
		Valid: false, Alias: "shop", Env: "prod",
		Errors: []PreviewError{
			{Code: "PUBLIC_SERVICE_MISSING", Detail: "no service has public:true"},
		},
	}
	f := newFakePreviewServer(t, http.StatusOK, canned)

	resp, err := Preview(PreviewOptions{
		APIURL:   f.srv.URL,
		APIToken: "test-token",
		Path:     dir,
		Alias:    "shop",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if resp.Valid {
		t.Errorf("want invalid: %+v", resp)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Code != "PUBLIC_SERVICE_MISSING" {
		t.Errorf("unexpected errors: %+v", resp.Errors)
	}
}

func TestPreview_LocalValidationFailsBeforeUpload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", "version: 99\nservices:\n  app:\n    build: .\n    port: 3000\n")

	f := newFakePreviewServer(t, http.StatusOK, PreviewResponse{Valid: true})
	_, err := Preview(PreviewOptions{
		APIURL:   f.srv.URL,
		APIToken: "test-token",
		Path:     dir,
		Alias:    "shop",
	})
	if err == nil {
		t.Fatal("expected local validation error")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Errorf("expected manifest validation message, got: %v", err)
	}
	if atomic.LoadInt32(&f.called) != 0 {
		t.Errorf("server should NOT have been called: %d", atomic.LoadInt32(&f.called))
	}
}

func TestPreview_AmbiguousManifestFailsBeforeUpload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", "version: 1\nservices:\n  app:\n    build: .\n    port: 3000\n")
	writeFile(t, dir, "dibbla.yml", "version: 1\nservices:\n  app:\n    build: .\n    port: 3000\n")
	f := newFakePreviewServer(t, http.StatusOK, PreviewResponse{Valid: true})
	_, err := Preview(PreviewOptions{
		APIURL:   f.srv.URL,
		APIToken: "test-token",
		Path:     dir,
		Alias:    "shop",
	})
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("expected ambiguous message, got: %v", err)
	}
	if atomic.LoadInt32(&f.called) != 0 {
		t.Errorf("server should NOT have been called: %d", atomic.LoadInt32(&f.called))
	}
}

func TestPreview_ServerReturnsHTTPError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")
	errResp := ErrorResponse{
		Status: "error",
		Error:  ErrorDetail{Code: "ARCHIVE_TOO_LARGE", Message: "archive exceeds 50 MB", RequestID: "req-123"},
	}
	f := newFakePreviewServer(t, http.StatusRequestEntityTooLarge, errResp)
	_, err := Preview(PreviewOptions{
		APIURL:   f.srv.URL,
		APIToken: "test-token",
		Path:     dir,
		Alias:    "shop",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ARCHIVE_TOO_LARGE") {
		t.Errorf("missing code in err: %v", err)
	}
}
