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
		if r.URL.Path == "/api/deploy/deployments/myapp/vcs/info" {
			// main is one push ahead of what runs, and that push's build failed.
			_, _ = w.Write([]byte(`{"default_branch":"main","latest_sha":"abcdefabcdefabcdefabcdefabcdefabcdefabcd","running_sha":"0123456789abcdef0123456789abcdef01234567",
				"main_deploy":{"operation_id":"deployment:op-9","phase":"failed","failure_code":"BUILD_FAILED","failure_summary":"exit code 7"}}`))
			return
		}
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
			"commit_sha":"0123456789abcdef0123456789abcdef01234567",
			"health_check":{"status":"healthy","response_time_ms":42},
			"services":[
				{"name":"web","replicas":2,"ready_replicas":2,"is_public":true,"status":"running"},
				{"name":"worker","replicas":1,"ready_replicas":1,"status":"running","stateful":true}
			]
		}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"myapp", "https://myapp.dibbla.com", "running", "web", "worker", "stateful", "2/2 ready", "required",
		"Commit:   0123456789abcdef0123456789abcdef01234567",
		"Main:     abcdefabcdefabcdefabcdefabcdefabcdefabcd — NOT running", "BUILD_FAILED — exit code 7", "dibbla deploy status deployment:op-9"} {
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
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", true, false); code != 0 {
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
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "ghost", false, false); code != 4 {
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
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, false); code != 3 {
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
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "Bad_Alias", false, false); code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Errorf("missing regex message: %q", stderr.String())
	}
	if hits.Load() != 0 {
		t.Errorf("server should NOT be called for an invalid alias: %d hits", hits.Load())
	}
}

// DIB-965: the security section says for which version the review holds,
// when the findings last changed, and whether anything changed since the
// agent last looked; `--review` prints the deployed REVIEW.md itself.
func TestRunAppsGetCore_SecuritySection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/deploy/deployments/myapp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"dep_4","alias":"myapp","url":"https://myapp.dibbla.com","status":"running",
			"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-09-21T10:00:00Z",
			"commit_sha":"ccc4567890ccc4567890ccc4567890ccc4567890",
			"review_status":"Ok","review_body":"---\nReview-status: Ok\n---\n# Report\n",
			"security":{
				"review":{"status":"Ok","summary":"Nothing to report","present":true,"reviewed_at":"2026-09-19T10:00:00Z","commit_sha":"aaa1234567aaa1234567aaa1234567aaa1234567","code_changed_since":true},
				"scan":{"status":"completed","deployment_id":"dep_4","started_at":"2026-09-21T10:00:00Z","completed_at":"2026-09-21T10:01:00Z",
					"vulnerabilities":{"critical":1,"high":4,"medium":12,"low":3,"negligible":0,"unknown":0,"total":20},"secrets":1,"images":1,"packages":529,
					"findings_changed_at":"2026-09-19T08:00:00Z"},
				"maintenance":{"configured":true,"enabled":true,"last_run_at":"2026-09-21T02:00:00Z","last_run_status":"completed","last_run_code":"NO_FINDING","last_change_at":"2026-09-19T08:00:00Z","changed_since_last_run":false,"pending_proposals":2}
			}
		}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Security:",
		"Review:  ✅ OK — for version aaa1234, written 2026-09-19",
		"Nothing to report",
		"the running version (ccc4567) is newer than the review",
		"REVIEW.md: dibbla apps get myapp --review",
		"Scan:    ❌ 1 leaked secret(s) · 1 critical · 4 high · 12 medium · 3 low",
		"findings last changed 2026-09-19",
		"Agent:   on — nothing has changed since it last looked",
		"last run 2026-09-21",
		"(NO_FINDING)",
		"2 proposal(s) waiting for a decision: dibbla apps proposals list myapp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}

	stdout.Reset()
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, true); code != 0 {
		t.Fatalf("--review exit %d (stderr=%q)", code, stderr.String())
	}
	if got := stdout.String(); !strings.HasPrefix(got, "---\nReview-status: Ok\n") || strings.Contains(got, "Security:") {
		t.Errorf("--review should print the REVIEW.md and nothing else, got:\n%s", got)
	}
}

func TestRunAppsGetCore_NoSecuritySectionFromAnOlderServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep_1","alias":"myapp","url":"https://myapp.dibbla.com","status":"running","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-20T10:00:00Z"}`))
	}))
	defer srv.Close()
	var stdout, stderr bytes.Buffer
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout.String(), "Security:") {
		t.Errorf("a server without the section must not be read as an app with no review:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := runAppsGetCore(&stdout, &stderr, srv.URL, "tok", "myapp", false, true); code != 1 || !strings.Contains(stderr.String(), "without a REVIEW.md") {
		t.Errorf("--review with no body: exit %d, stderr %q", code, stderr.String())
	}
}
