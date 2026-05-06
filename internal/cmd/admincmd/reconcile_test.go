package admincmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(t *testing.T, status int, body any, requireHeader string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/deploy/admin/reconcile" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		if requireHeader != "" && r.Header.Get("X-Admin-Token") != requireHeader {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func resetFlags() {
	reconcileJSON = false
}

func setupEnv(t *testing.T, url, adminToken string) {
	t.Helper()
	t.Setenv("DIBBLA_ADMIN_TOKEN", adminToken)
	t.Setenv("DIBBLA_API_URL", url)
	// Force CI mode so config.Load skips keyring lookups in tests.
	t.Setenv("CI", "1")
	t.Setenv("DIBBLA_API_TOKEN", "user-tok")
}

func TestRunReconcile_MissingAdminTokenExitsOne(t *testing.T) {
	resetFlags()
	t.Setenv("DIBBLA_ADMIN_TOKEN", "")
	t.Setenv("CI", "1")
	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "set DIBBLA_ADMIN_TOKEN") {
		t.Errorf("missing prompt: %q", stderr.String())
	}
}

func TestRunReconcile_HappyPath_HumanOutput(t *testing.T) {
	resetFlags()
	srv := newServer(t, http.StatusOK, reconcileResult{
		Deployments: []string{"myapp-old-worker", "otherapp-redis"},
		Services:    []string{"myapp-old-worker"},
		Ingresses:   nil,
	}, "secret123")
	setupEnv(t, srv.URL, "secret123")

	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "orphan sweep complete") {
		t.Errorf("missing summary line: %q", out)
	}
	if !strings.Contains(out, "deployments: 2") {
		t.Errorf("missing deployments count: %q", out)
	}
	if !strings.Contains(out, "myapp-old-worker") {
		t.Errorf("missing deployment name: %q", out)
	}
	if !strings.Contains(out, "ingresses:   0") {
		t.Errorf("missing ingresses: %q", out)
	}
}

func TestRunReconcile_JSONPassthrough(t *testing.T) {
	resetFlags()
	reconcileJSON = true
	defer resetFlags()
	srv := newServer(t, http.StatusOK, reconcileResult{
		Deployments: []string{"x"}, Services: nil, Ingresses: nil,
	}, "tok")
	setupEnv(t, srv.URL, "tok")

	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var out reconcileResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, stdout.String())
	}
	if len(out.Deployments) != 1 || out.Deployments[0] != "x" {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestRunReconcile_Unauthorized(t *testing.T) {
	resetFlags()
	srv := newServer(t, http.StatusOK, reconcileResult{}, "right-token")
	setupEnv(t, srv.URL, "wrong-token")

	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "unauthorized") {
		t.Errorf("missing unauthorized line: %q", stderr.String())
	}
}

func TestRunReconcile_EndpointNotEnabled(t *testing.T) {
	resetFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	setupEnv(t, srv.URL, "tok")

	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "admin endpoints not enabled") {
		t.Errorf("missing 404 hint: %q", stderr.String())
	}
}

func TestRunReconcile_ServiceUnavailable(t *testing.T) {
	resetFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "reconciler not configured", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	setupEnv(t, srv.URL, "tok")

	var stdout, stderr bytes.Buffer
	if code := runReconcile(&stdout, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "reconciler not configured") {
		t.Errorf("missing message: %q", stderr.String())
	}
}
