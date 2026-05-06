package apps

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceNameRe(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"web", true},
		{"my-worker", true},
		{"x", true},
		{"", false},
		{"Web", false},
		{"-leading-dash", false},
		{"a..b", false},
		{"toolong-aaaaaaaaaaaaaaaaaaaaaaaaa", false}, // > 30 chars
	}
	for _, c := range cases {
		got := ServiceNameRe.MatchString(c.name)
		if got != c.ok {
			t.Errorf("%q: got %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestRestartService_HappyPath(t *testing.T) {
	var sawPath, sawAuth, sawMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		sawMethod = r.Method
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias":   "myapp",
			"service": "worker",
			"status":  "restarted",
			"message": "rolling restart triggered",
		})
	}))
	defer srv.Close()

	resp, err := RestartService(srv.URL, "tok", "myapp", "worker")
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if sawMethod != "POST" {
		t.Errorf("method: %q", sawMethod)
	}
	if sawPath != "/api/deploy/deployments/myapp/services/worker/restart" {
		t.Errorf("path: %q", sawPath)
	}
	if sawAuth != "Bearer tok" {
		t.Errorf("auth: %q", sawAuth)
	}
	if resp.Alias != "myapp" || resp.Service != "worker" || resp.Status != "restarted" {
		t.Errorf("response: %+v", resp)
	}
}

func TestRestartService_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"error": map[string]any{
				"code":    "NOT_FOUND",
				"message": "service worker not found in deployment myapp",
			},
		})
	}))
	defer srv.Close()
	_, err := RestartService(srv.URL, "tok", "myapp", "worker")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("missing code: %v", err)
	}
}

func TestRestartService_RejectsBadServiceName(t *testing.T) {
	_, err := RestartService("http://api", "tok", "myapp", "Web")
	if err == nil {
		t.Fatal("expected error for bad service name")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestRestartService_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"error":  map[string]any{"code": "INTERNAL_ERROR", "message": "kube went away"},
		})
	}))
	defer srv.Close()
	_, err := RestartService(srv.URL, "tok", "myapp", "worker")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "INTERNAL_ERROR") {
		t.Errorf("missing code: %v", err)
	}
}
