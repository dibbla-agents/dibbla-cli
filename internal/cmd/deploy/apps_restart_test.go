package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRestartServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAppsRestartCore_HappyHumanOutput(t *testing.T) {
	srv := newRestartServer(t, http.StatusOK, map[string]any{
		"alias": "myapp", "service": "worker", "status": "restarted",
		"message": "rolling restart triggered",
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "worker", false, false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "rolling restart triggered") {
		t.Errorf("missing success line: %q", out)
	}
	if !strings.Contains(out, "myapp/worker") {
		t.Errorf("missing alias/service: %q", out)
	}
}

func TestRunAppsRestartCore_QuietPrintsAlias(t *testing.T) {
	srv := newRestartServer(t, http.StatusOK, map[string]any{
		"alias": "myapp", "service": "worker", "status": "restarted",
		"message": "ok",
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "worker", true, false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "myapp" {
		t.Errorf("quiet output: %q", got)
	}
}

func TestRunAppsRestartCore_JSON(t *testing.T) {
	srv := newRestartServer(t, http.StatusOK, map[string]any{
		"alias": "myapp", "service": "web", "status": "restarted", "message": "ok",
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "web", false, true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, stdout.String())
	}
	if got["alias"] != "myapp" || got["service"] != "web" {
		t.Errorf("unexpected JSON: %+v", got)
	}
}

func TestRunAppsRestartCore_ServiceNotFound(t *testing.T) {
	srv := newRestartServer(t, http.StatusNotFound, map[string]any{
		"status": "error",
		"error":  map[string]any{"code": "NOT_FOUND", "message": "service worker not found"},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "worker", false, false); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "service not found") {
		t.Errorf("missing 404 message: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "dibbla apps list") {
		t.Errorf("missing hint: %q", stderr.String())
	}
}

func TestRunAppsRestartCore_BadServiceNameRejectedLocally(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "Web", false, false); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Errorf("missing regex message: %q", stderr.String())
	}
	if hits != 0 {
		t.Errorf("server should NOT be called for invalid service name: %d", hits)
	}
}

func TestRunAppsRestartCore_GenericFailure(t *testing.T) {
	srv := newRestartServer(t, http.StatusInternalServerError, map[string]any{
		"status": "error",
		"error":  map[string]any{"code": "INTERNAL_ERROR", "message": "kube went away"},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsRestartCore(&stdout, &stderr, srv.URL, "tok", "myapp", "worker", false, false); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "restart failed") {
		t.Errorf("missing failure line: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "INTERNAL_ERROR") {
		t.Errorf("missing code: %q", stderr.String())
	}
}
