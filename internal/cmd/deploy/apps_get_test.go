package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunAppsGetCore_HumanOutput(t *testing.T) {
	deployed := "2026-08-20T10:00:00Z"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/deploy/deployments/myapp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"dep_1","alias":"myapp","url":"https://myapp.dibbla.com","status":"running",
			"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-20T10:00:00Z","deployed_at":"` + deployed + `",
			"require_login":true,"app_access_policy":"all_members",
			"replicas":2,"cpu":"500m","memory":"512Mi",
			"health_check":{"status":"healthy","response_time_ms":42},
			"services":[
				{"name":"web","replicas":2,"ready_replicas":2,"is_public":true,"status":"running"},
				{"name":"worker","replicas":1,"ready_replicas":1,"status":"running","stateful":true}
			]
		}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"myapp", "https://myapp.dibbla.com", "running", "web", "worker", "stateful", "2/2 ready", "required"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRunAppsGetCore_JSONIsVerbatim(t *testing.T) {
	body := `{"id":"dep_1","alias":"myapp","url":"https://myapp.dibbla.com","status":"running","future_field":{"x":1}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", true); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, stdout.String())
	}
	if _, ok := got["future_field"]; !ok {
		t.Errorf("verbatim output should keep unknown server fields: %s", stdout.String())
	}
	if got["alias"] != "myapp" {
		t.Errorf("unexpected JSON: %s", stdout.String())
	}
}

func TestRunAppsGetCore_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"NOT_FOUND","message":"Deployment not found: ghost"}}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "ghost", false); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(stderr.String(), "NOT_FOUND") {
		t.Errorf("missing server code: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "dibbla apps list") {
		t.Errorf("missing hint: %q", stderr.String())
	}
}

func TestRunAppsGetCore_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"UNAUTHORIZED","message":"bad token"}}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
}

func TestRunAppsGetCore_BadAliasRejectedLocally(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "Bad_Alias", false); code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Errorf("missing regex message: %q", stderr.String())
	}
	if hits.Load() != 0 {
		t.Errorf("server should NOT be called for an invalid alias: %d hits", hits.Load())
	}
}
