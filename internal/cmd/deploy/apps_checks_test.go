package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChecksAPI is a scripted deploy-api: routes are keyed by "METHOD /path"
// and every request is counted, so tests can prove locally rejected input
// makes zero requests.
type fakeChecksAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	hits   map[string]int
}

func newFakeChecksAPI(t *testing.T, routes map[string]http.HandlerFunc) *fakeChecksAPI {
	t.Helper()
	f := &fakeChecksAPI{routes: routes, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.hits[key]++
		handler, ok := f.routes[key]
		f.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"status": "error",
				"error":  map[string]any{"code": "APPLICATION_CHECK_NOT_FOUND", "message": "Application Checks resource not found"},
			})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeChecksAPI) url() string { return f.srv.URL }

func (f *fakeChecksAPI) hit(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func (f *fakeChecksAPI) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := 0
	for _, n := range f.hits {
		sum += n
	}
	return sum
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errEnvelope(code, message string) map[string]any {
	return map[string]any{"status": "error", "error": map[string]any{"code": code, "message": message}}
}

func executionDoc(id, status string) map[string]any {
	return map[string]any{
		"execution_id": id, "organization_id": "org_1", "deployment_id": "dep_1",
		"trigger": "manual", "status": status, "run_id": "run_" + id,
		"workflow_revision": "acv1", "config_revision": "rev_1a2b3c4d5e6f",
		"requested_check_ids": []string{"home-page"}, "reservation_id": "res_1",
		"tool_allowlist": []string{"http"}, "lease_epoch": 1,
		"deadline_at": "2026-08-25T23:00:00Z",
		"started_at":  "2026-08-25T10:00:00Z", "finished_at": "2026-08-25T10:00:09Z",
	}
}

func runRoute(exec map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]any{"execution": exec})
	}
}

func fastPolling(t *testing.T) {
	t.Helper()
	oldInterval, oldTimeout := checkPollInterval, checkRunTimeout
	checkPollInterval, checkRunTimeout = 2*time.Millisecond, time.Minute
	t.Cleanup(func() { checkPollInterval, checkRunTimeout = oldInterval, oldTimeout })
}

// ---------------------------------------------------------------- list

func TestRunAppsChecksListCore_HumanOutput(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "deployment_id": "dep_1", "deployment_alias": "myapp",
				"configured": true, "config_revision": "sha256:abcdef123456",
				"settings": map[string]any{"enabled": true, "version": 4},
				"definitions": []map[string]any{
					{"id": "home-page", "name": "Home page", "kind": "http", "schedule": "nightly",
						"classification": "read_only_deterministic", "enabled": true, "schema_version": 1},
					{"id": "assistant-answer", "name": "Assistant answer", "kind": "semantic", "schedule": "nightly",
						"classification": "semantic", "enabled": false, "schema_version": 1},
				},
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"myapp", "Runtime: enabled", "home-page", "assistant-answer", "read_only_deterministic", "semantic", "Home page"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunAppsChecksListCore_NotConfiguredIsExitZero(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "deployment_alias": "myapp", "configured": false, "definitions": []any{},
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d, want 0 — no checks is an answer, not an error", code)
	}
	if !strings.Contains(stdout.String(), "Configured: no") {
		t.Errorf("missing not-configured line: %q", stdout.String())
	}
}

func TestRunAppsChecksListCore_JSONVerbatim(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "deployment_alias": "myapp", "configured": true,
				"definitions": []any{}, "future_field": "kept",
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "myapp", true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if got["future_field"] != "kept" {
		t.Errorf("verbatim output should keep unknown server fields: %s", stdout.String())
	}
}

func TestRunAppsChecksListCore_OrgDisabledIsNotFound(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, errEnvelope("APPLICATION_CHECKS_DISABLED", "Application Checks are not enabled for this organization"))
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	for _, want := range []string{"APPLICATION_CHECKS_DISABLED", "not enabled for this organization"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("missing %q: %q", want, stderr.String())
		}
	}
	// The app is not what went missing — the alias hint would be a wrong
	// answer here.
	if strings.Contains(stderr.String(), "apps list") {
		t.Errorf("org-disabled 404 must not hint at wrong aliases: %q", stderr.String())
	}
}

func TestRunAppsChecksListCore_Forbidden(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusForbidden, errEnvelope("FORBIDDEN", "viewer role required"))
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
}

func TestRunAppsChecksListCore_BadAliasRejectedLocally(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	if code := runAppsChecksListCore(&stdout, &stderr, api.url(), "tok", "Bad_Alias", false); code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Errorf("missing regex message: %q", stderr.String())
	}
	if api.total() != 0 {
		t.Errorf("server should NOT be called for an invalid alias: %d hits", api.total())
	}
}

// ---------------------------------------------------------------- run

func TestRunAppsChecksRunCore_SyncOutcomeCodes(t *testing.T) {
	fastPolling(t)
	cases := []struct {
		status string
		want   int
	}{
		{"pass", 0},
		{"fail", 8},
		{"error", 9},
		{"indeterminate", 10},
		{"canceled", 12},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			pollKey := "GET /api/deploy/deployments/myapp/application-check-executions/acx_1"
			api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
				"POST /api/deploy/deployments/myapp/application-checks/run": runRoute(executionDoc("acx_1", "running")),
				pollKey: func(w http.ResponseWriter, r *http.Request) {
					exec := executionDoc("acx_1", tc.status)
					exec["terminal_code"] = "ASSERTION_FAILED"
					writeJSON(w, http.StatusOK, exec)
				},
			})

			var stdout, stderr bytes.Buffer
			code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeSync, false, false, time.Now)
			if code != tc.want {
				t.Fatalf("exit %d, want %d (stderr=%q)", code, tc.want, stderr.String())
			}
			if api.hit(pollKey) != 1 {
				t.Errorf("expected exactly one poll, got %d", api.hit(pollKey))
			}
			if !strings.Contains(stdout.String(), tc.status) {
				t.Errorf("missing outcome %q in:\n%s", tc.status, stdout.String())
			}
			if !strings.Contains(stdout.String(), "ASSERTION_FAILED") {
				t.Errorf("missing terminal code: %q", stdout.String())
			}
		})
	}
}

func TestRunAppsChecksRunCore_SkippedConcurrentImmediate(t *testing.T) {
	fastPolling(t)
	pollKey := "GET /api/deploy/deployments/myapp/application-check-executions/acx_1"
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/application-checks/run": runRoute(executionDoc("acx_1", "skipped_concurrent")),
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeSync, false, false, time.Now); code != 13 {
		t.Fatalf("exit %d, want 13", code)
	}
	if api.hit(pollKey) != 0 {
		t.Errorf("a terminal acceptance must not be polled: %d hits", api.hit(pollKey))
	}
	if !strings.Contains(stdout.String(), "skipped_concurrent") {
		t.Errorf("missing outcome: %q", stdout.String())
	}
}

func TestRunAppsChecksRunCore_JSONSyncOneDocumentWithOutcomeAndExitCode(t *testing.T) {
	fastPolling(t)
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/application-checks/run": runRoute(executionDoc("acx_1", "running")),
		"GET /api/deploy/deployments/myapp/application-check-executions/acx_1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, executionDoc("acx_1", "fail"))
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeSync, false, true, time.Now); code != 8 {
		t.Fatalf("exit %d, want 8", code)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	documents := 0
	var last map[string]any
	for decoder.More() {
		if err := decoder.Decode(&last); err != nil {
			t.Fatalf("decode: %v\n%s", err, stdout.String())
		}
		documents++
	}
	if documents != 1 {
		t.Errorf("sync --json must emit exactly one document, got %d:\n%s", documents, stdout.String())
	}
	if last["outcome"] != "fail" || last["exit_code"] != float64(8) {
		t.Errorf("terminal doc must carry outcome + implied exit code: %v", last)
	}
	exec, _ := last["execution"].(map[string]any)
	if exec["execution_id"] != "acx_1" {
		t.Errorf("terminal doc must embed the execution: %v", last)
	}
}

func TestRunAppsChecksRunCore_AsyncModes(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/application-checks/run": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusAccepted, map[string]any{"execution": executionDoc("acx_9", "running"), "request_id": "req_1"})
		},
	})

	t.Run("json prints parent execution only", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeAsync, false, true, time.Now); code != 0 {
			t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
		}
		var got map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v\n%s", err, stdout.String())
		}
		if got["execution_id"] != "acx_9" {
			t.Errorf("async --json must print the parent execution: %s", stdout.String())
		}
		if _, ok := got["request_id"]; ok {
			t.Errorf("async --json must drop the transport wrapper: %s", stdout.String())
		}
		if api.total() != 1 {
			t.Errorf("async must not poll: %d hits", api.total())
		}
	})

	t.Run("quiet prints execution id", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeAsync, true, false, time.Now); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if got := strings.TrimSpace(stdout.String()); got != "acx_9" {
			t.Errorf("quiet output %q, want execution id", got)
		}
	})

	t.Run("human names the execution", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeAsync, false, false, time.Now); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(stdout.String(), "acx_9 accepted") {
			t.Errorf("missing accepted line: %q", stdout.String())
		}
	})
}

func TestRunAppsChecksRunCore_FollowJSONNDJSON(t *testing.T) {
	fastPolling(t)
	pollKey := "GET /api/deploy/deployments/myapp/application-check-executions/acx_1"
	responses := []string{"running", "fail"}
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/application-checks/run": runRoute(executionDoc("acx_1", "running")),
		pollKey: func(w http.ResponseWriter, r *http.Request) {
			status := "running"
			if len(responses) > 0 {
				status = responses[0]
				responses = responses[1:]
			}
			writeJSON(w, http.StatusOK, executionDoc("acx_1", status))
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeFollow, false, true, time.Now); code != 8 {
		t.Fatalf("exit %d, want 8 (stderr=%q)", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want created + one status change + summary (3 lines), got %d:\n%s", len(lines), stdout.String())
	}
	var created, statusChange, summary map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &created); err != nil {
		t.Fatalf("line 1 not JSON: %v (%s)", err, lines[0])
	}
	if err := json.Unmarshal([]byte(lines[1]), &statusChange); err != nil {
		t.Fatalf("line 2 not JSON: %v (%s)", err, lines[1])
	}
	if err := json.Unmarshal([]byte(lines[2]), &summary); err != nil {
		t.Fatalf("line 3 not JSON: %v (%s)", err, lines[2])
	}
	if created["type"] != "execution_created" || created["status"] != "running" {
		t.Errorf("created line wrong: %v", created)
	}
	if statusChange["type"] != "execution_status" || statusChange["status"] != "fail" {
		t.Errorf("status line wrong: %v", statusChange)
	}
	if summary["type"] != "summary" || summary["outcome"] != "fail" || summary["exit_code"] != float64(8) {
		t.Errorf("terminal summary must carry outcome + implied exit code: %v", summary)
	}
}

func TestRunAppsChecksRunCore_TransportCodes(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{403, 3},
		{400, 5},
		{409, 6},
		{408, 7},
		{500, 1},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("http_%d", tc.status), func(t *testing.T) {
			api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
				"POST /api/deploy/deployments/myapp/application-checks/run": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, tc.status, errEnvelope("SOME_CODE", "transport problem"))
				},
			})
			var stdout, stderr bytes.Buffer
			if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeSync, false, false, time.Now); code != tc.want {
				t.Fatalf("exit %d, want %d", code, tc.want)
			}
			if !strings.Contains(stderr.String(), "SOME_CODE") {
				t.Errorf("stable code must reach stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunAppsChecksRunCore_BadCheckIDRejectedLocally(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "Bad_ID", runModeSync, false, false, time.Now); code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if api.total() != 0 {
		t.Errorf("server should NOT be called for an invalid check id: %d hits", api.total())
	}
}

func TestRunAppsChecksRunCore_PollTimeout(t *testing.T) {
	oldInterval, oldTimeout := checkPollInterval, checkRunTimeout
	checkPollInterval, checkRunTimeout = 2*time.Millisecond, time.Millisecond
	t.Cleanup(func() { checkPollInterval, checkRunTimeout = oldInterval, oldTimeout })

	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/application-checks/run": runRoute(executionDoc("acx_slow", "running")),
		"GET /api/deploy/deployments/myapp/application-check-executions/acx_slow": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, executionDoc("acx_slow", "running"))
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "", runModeSync, false, false, time.Now); code != 7 {
		t.Fatalf("exit %d, want 7 (timeout)", code)
	}
	if !strings.Contains(stderr.String(), "acx_slow") {
		t.Errorf("timeout message must name the execution: %q", stderr.String())
	}
}

func TestRunAppsChecksRunCore_FlagGroupsMutuallyExclusive(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"quiet and json", []string{"--quiet", "--json"}},
		{"async and follow", []string{"--async", "--follow"}},
	}
	flags := appsChecksRunCmd.Flags()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Start from a clean slate: a previous Parse keeps values set.
			for _, f := range []string{"quiet", "json", "async", "follow"} {
				_ = flags.Set(f, "false")
			}
			if err := flags.Parse(tc.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := appsChecksRunCmd.ValidateFlagGroups(); err == nil {
				t.Errorf("%v must be mutually exclusive", tc.args)
			}
		})
	}
}

// ---------------------------------------------------------------- history

func TestRunAppsChecksHistoryCore_SingleCheckHuman(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"runs": []map[string]any{
					{"run_id": "acr_2", "check_id": "home-page", "outcome": "pass", "code": "",
						"summary": "all good", "started_at": "2026-08-25T11:00:00Z", "schema_version": 1},
					{"run_id": "acr_1", "check_id": "home-page", "outcome": "fail", "code": "ASSERTION_FAILED",
						"summary": "Expected citations, received none", "started_at": "2026-08-24T01:00:00Z", "schema_version": 1},
				},
				"next_cursor": "cur_9",
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "home-page", 0, 0, false, time.Now); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"home-page", "ASSERTION_FAILED", "Expected citations", "pass", "fail"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "pass") > strings.Index(out, "fail") {
		t.Errorf("newest run must come first:\n%s", out)
	}
}

func TestRunAppsChecksHistoryCore_SingleCheckJSONVerbatim(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"runs":         []any{},
				"next_cursor":  "cur_9",
				"future_field": true,
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "home-page", 0, 0, true, time.Now); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if got["next_cursor"] != "cur_9" || got["future_field"] != true {
		t.Errorf("history --json must be the server page verbatim: %s", stdout.String())
	}
}

func TestRunAppsChecksHistoryCore_MergedAcrossChecks(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "configured": true,
				"definitions": []map[string]any{
					{"id": "home-page", "kind": "http"}, {"id": "assistant-answer", "kind": "semantic"},
				},
			})
		},
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"runs": []map[string]any{
				{"run_id": "acr_a", "check_id": "home-page", "outcome": "pass", "started_at": "2026-08-25T11:00:00Z"},
				{"run_id": "acr_b", "check_id": "home-page", "outcome": "fail", "started_at": "2026-08-01T01:00:00Z"},
			}})
		},
		"GET /api/deploy/deployments/myapp/application-checks/assistant-answer/runs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"runs": []map[string]any{
				{"run_id": "acr_c", "check_id": "assistant-answer", "outcome": "error", "started_at": "2026-08-25T10:00:00Z"},
			}})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "", 24*time.Hour, 0, true, func() time.Time { return now }); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var doc struct {
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	// --since 24h drops acr_b (2026-08-01); the rest stay newest-first.
	wantOrder := []string{"acr_a", "acr_c"}
	if len(doc.Runs) != len(wantOrder) {
		t.Fatalf("runs after --since 24h: %+v", doc.Runs)
	}
	for i, id := range wantOrder {
		if doc.Runs[i].RunID != id {
			t.Errorf("run %d = %s, want %s (newest first)", i, doc.Runs[i].RunID, id)
		}
	}
}

func TestRunAppsChecksHistoryCore_LimitSentToServer(t *testing.T) {
	var gotQuery string
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			writeJSON(w, http.StatusOK, map[string]any{"runs": []any{}})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "home-page", 0, 25, false, time.Now); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if gotQuery != "limit=25" {
		t.Errorf("limit must reach the server as a page-size param, got %q", gotQuery)
	}
}

func TestRunAppsChecksHistoryCore_NoChecksConfigured(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "configured": false, "definitions": []any{},
			})
		},
	})

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "", 0, 0, false, time.Now); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "no checks configured") {
		t.Errorf("missing empty-state line: %q", stdout.String())
	}
	if api.total() != 1 {
		t.Errorf("only the definitions call should happen, got %d hits", api.total())
	}
}

func TestRunAppsChecksHistoryCore_NotFound(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, errEnvelope("APPLICATION_CHECK_NOT_FOUND", "Application Checks resource not found"))
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "home-page", 0, 0, false, time.Now); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
}

func TestRunAppsChecksHistoryCore_BadCheckIDRejectedLocally(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "BAD", 0, 0, false, time.Now); code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if api.total() != 0 {
		t.Errorf("server should NOT be called for an invalid check id: %d hits", api.total())
	}
}

// ---------------------------------------------------------------- enable / disable

func TestRunAppsChecksToggleCore_EnableAndDisable(t *testing.T) {
	cases := []struct {
		name   string
		enable bool
	}{
		{"enable", true},
		{"disable", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
				"PUT /api/deploy/deployments/myapp/application-check-settings": func(w http.ResponseWriter, r *http.Request) {
					var err error
					body, err = io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read body: %v", err)
					}
					writeJSON(w, http.StatusOK, map[string]any{
						"organization_id": "org_1", "scope_type": "app", "scope_id": "dep_1",
						"enabled": tc.enable, "version": 3,
					})
				},
			})

			var stdout, stderr bytes.Buffer
			code := runAppsChecksToggleCore(&stdout, &stderr, api.url(), "tok", "myapp", "", true, tc.enable, func(string) bool { return true })
			if code != 0 {
				t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
			}

			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("request body not JSON: %v (%s)", err, body)
			}
			if sent["enabled"] != tc.enable {
				t.Errorf("PUT body enabled=%v, want %v", sent["enabled"], tc.enable)
			}
			wantState := "enabled"
			if !tc.enable {
				wantState = "disabled"
			}
			if !strings.Contains(stdout.String(), wantState) {
				t.Errorf("missing %q in: %q", wantState, stdout.String())
			}
		})
	}
}

func TestRunAppsChecksToggleCore_ConfirmDeclinedMakesZeroRequests(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	code := runAppsChecksToggleCore(&stdout, &stderr, api.url(), "tok", "myapp", "", false, true, func(string) bool { return false })
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Cancelled.") {
		t.Errorf("missing cancelled line: %q", stdout.String())
	}
	if api.total() != 0 {
		t.Errorf("a declined prompt must make zero requests: %d hits", api.total())
	}
}

func TestRunAppsChecksToggleCore_CheckFlagRejectedLocally(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	code := runAppsChecksToggleCore(&stdout, &stderr, api.url(), "tok", "myapp", "home-page", true, true, func(string) bool { return true })
	if code != 5 {
		t.Fatalf("exit %d, want 5", code)
	}
	if !strings.Contains(stderr.String(), "per-check enablement") {
		t.Errorf("missing explanation: %q", stderr.String())
	}
	if api.total() != 0 {
		t.Errorf("server should NOT be called when --check is rejected: %d hits", api.total())
	}
}

func TestRunAppsChecksToggleCore_ServerErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   int
	}{
		{"forbidden", 403, "FORBIDDEN_CONFIG", 3},
		{"not configured", 400, "VALIDATION_FAILED", 5},
		{"version conflict", 409, "APPLICATION_CHECKS_SETTINGS_VERSION_CONFLICT", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
				"PUT /api/deploy/deployments/myapp/application-check-settings": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, tc.status, errEnvelope(tc.code, "nope"))
				},
			})
			var stdout, stderr bytes.Buffer
			code := runAppsChecksToggleCore(&stdout, &stderr, api.url(), "tok", "myapp", "", true, true, func(string) bool { return true })
			if code != tc.want {
				t.Fatalf("exit %d, want %d", code, tc.want)
			}
			if !strings.Contains(stderr.String(), tc.code) {
				t.Errorf("stable code must reach stderr: %q", stderr.String())
			}
		})
	}
}
