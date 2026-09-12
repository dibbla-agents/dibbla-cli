package deploy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type recordedRequest struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// newReleasesServer answers every request with status/body and records the
// last request it saw.
func newReleasesServer(t *testing.T, status int, body string) (*httptest.Server, *recordedRequest, *int32) {
	t.Helper()
	var hits int32
	last := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		b, _ := io.ReadAll(r.Body)
		*last = recordedRequest{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, last, &hits
}

const releasesDoc = `{"alias":"myapp","deployment_exists":true,"running_deployment_id":"dep_c",
"previous_deployment_id":"dep_b","registry_reachable":true,"future_field":1,
"releases":[
 {"deployment_id":"dep_c","image":"reg/myapp-h1:dep_c","digest":"sha256:cccccccccccccccc","created_at":"2026-09-12T10:00:00Z","running":true,"available":true,"has_config":true,"author_email":"dev@acme.test"},
 {"deployment_id":"dep_b","image":"reg/myapp-h1:dep_b","digest":"sha256:bbbbbbbbbbbbbbbb","created_at":"2026-09-11T10:00:00Z","running":false,"available":true,"has_config":true},
 {"deployment_id":"dep_a","image":"reg/myapp-h1:dep_a","created_at":"2026-09-10T10:00:00Z","running":false,"available":false,"has_config":true}
]}`

func TestRunAppsReleasesCore_HumanTable(t *testing.T) {
	srv, last, _ := newReleasesServer(t, http.StatusOK, releasesDoc)
	var stdout, stderr bytes.Buffer
	if code := runAppsReleasesCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if last.Method != "GET" || last.Path != "/api/deploy/deployments/myapp/releases" || last.Auth != "Bearer tok" {
		t.Fatalf("request: %+v", *last)
	}
	out := stdout.String()
	for _, want := range []string{"3 release(s)", "dep_c", "running+config", "dep_b", "available+config", "dep_a", "gone", "cccccccccccc", "dev@acme.test", "dibbla apps rollback myapp", "(→ dep_b)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "deployment is missing") {
		t.Fatalf("live app must not warn about a missing deployment:\n%s", out)
	}
}

func TestRunAppsReleasesCore_JSONIsVerbatim(t *testing.T) {
	srv, _, _ := newReleasesServer(t, http.StatusOK, releasesDoc)
	var stdout, stderr bytes.Buffer
	if code := runAppsReleasesCore(&stdout, &stderr, srv.URL, "tok", "myapp", true); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"future_field":1`) {
		t.Fatalf("--json must emit the server document verbatim:\n%s", stdout.String())
	}
}

func TestRunAppsReleasesCore_MissingDeploymentIsExplained(t *testing.T) {
	doc := `{"alias":"myapp","deployment_exists":false,"previous_deployment_id":"dep_b","registry_reachable":true,
"releases":[{"deployment_id":"dep_b","image":"reg/myapp-h1:dep_b","running":false,"available":true,"has_config":true}]}`
	srv, _, _ := newReleasesServer(t, http.StatusOK, doc)
	var stdout, stderr bytes.Buffer
	if code := runAppsReleasesCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "deployment is missing") || !strings.Contains(stdout.String(), "--to <dep-id>") {
		t.Fatalf("expected the recreate hint:\n%s", stdout.String())
	}
}

func TestRunAppsReleasesCore_NotFoundAndInvalidAlias(t *testing.T) {
	srv, _, hits := newReleasesServer(t, http.StatusNotFound, `{"status":"error","error":{"code":"NOT_FOUND","message":"deployment not found: myapp"}}`)
	var stdout, stderr bytes.Buffer
	if code := runAppsReleasesCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 4 {
		t.Fatalf("exit %d, want 4 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "NOT_FOUND") || !strings.Contains(stderr.String(), "dibbla apps list") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	before := atomic.LoadInt32(hits)
	if code := runAppsReleasesCore(&stdout, &stderr, srv.URL, "tok", "Bad Alias", false); code != 5 || atomic.LoadInt32(hits) != before {
		t.Fatalf("invalid alias: exit %d, requests made %d", code, atomic.LoadInt32(hits)-before)
	}
}

func yes(string) (bool, error) { return true, nil }

func TestRunAppsRollbackCore_DefaultPreviousRelease(t *testing.T) {
	srv, last, _ := newReleasesServer(t, http.StatusOK, `{"alias":"myapp","status":"rolled_back","deployment_id":"dep_b","previous_deployment_id":"dep_c","image":"reg/myapp-h1:dep_b","recreated":false,"message":"rolled back to dep_b"}`)
	var stdout, stderr bytes.Buffer
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "", true, false, yes); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if last.Method != "POST" || last.Path != "/api/deploy/deployments/myapp/rollback" || last.Auth != "Bearer tok" || last.Body != "" {
		t.Fatalf("request: %+v", *last)
	}
	out := stdout.String()
	for _, want := range []string{"rolled back to dep_b", "was:   dep_c", "reg/myapp-h1:dep_b", "dibbla apps get myapp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunAppsRollbackCore_ToNamedReleaseSendsBody(t *testing.T) {
	srv, last, _ := newReleasesServer(t, http.StatusOK, `{"alias":"myapp","status":"rolled_back","deployment_id":"dep_a","image":"reg/myapp-h1:dep_a","recreated":true,"message":"deployment recreated"}`)
	var stdout, stderr bytes.Buffer
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "dep_a", true, false, yes); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(last.Body), &body); err != nil || body["to"] != "dep_a" {
		t.Fatalf("body: %q", last.Body)
	}
	if !strings.Contains(stdout.String(), "recreated from release dep_a") {
		t.Fatalf("recreate should be said:\n%s", stdout.String())
	}
}

func TestRunAppsRollbackCore_ConfirmationAndLocalValidation(t *testing.T) {
	srv, _, hits := newReleasesServer(t, http.StatusOK, `{}`)
	var stdout, stderr bytes.Buffer

	// Declined: no request.
	no := func(string) (bool, error) { return false, nil }
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "", false, false, no); code != 0 || !strings.Contains(stdout.String(), "Cancelled.") {
		t.Fatalf("declined: exit %d out %q", code, stdout.String())
	}
	// No terminal: refused with 5, no request.
	notTTY := func(string) (bool, error) { return false, io.EOF }
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "", false, false, notTTY); code != 5 {
		t.Fatalf("no tty: exit %d", code)
	}
	// Malformed --to: refused with 5, no request, and the prompt is never shown.
	prompted := false
	spy := func(string) (bool, error) { prompted = true; return true, nil }
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "v1", false, false, spy); code != 5 || prompted {
		t.Fatalf("bad --to: exit %d prompted=%v", code, prompted)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("expected zero requests, got %d", n)
	}
}

func TestRunAppsRollbackCore_ReleaseGoneHint(t *testing.T) {
	srv, _, _ := newReleasesServer(t, http.StatusGone, `{"status":"error","error":{"code":"RELEASE_GONE","message":"release dep_a of myapp is no longer in the registry (swept by retention); still available: dep_c, dep_b"}}`)
	var stdout, stderr bytes.Buffer
	code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "dep_a", true, false, yes)
	if code == 0 {
		t.Fatal("expected a failure exit")
	}
	if !strings.Contains(stderr.String(), "RELEASE_GONE") || !strings.Contains(stderr.String(), "dep_c, dep_b") || !strings.Contains(stderr.String(), "dibbla apps releases myapp") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}

func TestRunAppsRollbackCore_UnchangedAndJSON(t *testing.T) {
	srv, _, _ := newReleasesServer(t, http.StatusOK, `{"alias":"myapp","status":"unchanged","deployment_id":"dep_c","image":"reg/myapp-h1:dep_c","recreated":false,"message":"already running","extra":true}`)
	var stdout, stderr bytes.Buffer
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "dep_c", true, false, yes); code != 0 || !strings.Contains(stdout.String(), "already runs dep_c") {
		t.Fatalf("exit %d out %q", code, stdout.String())
	}
	stdout.Reset()
	if code := runAppsRollbackCore(&stdout, &stderr, srv.URL, "tok", "myapp", "dep_c", true, true, yes); code != 0 || !strings.Contains(stdout.String(), `"extra":true`) {
		t.Fatalf("--json: exit %d out %q", code, stdout.String())
	}
}
