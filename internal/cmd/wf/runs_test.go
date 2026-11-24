package wf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// runsStub answers the slim runs routes the way the server does.
func runsStub(t *testing.T, status int, body string, gotPath *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			*gotPath = r.Method + " " + r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	previous := apiClient
	apiClient = apiclient.NewClient(srv.URL, "test-token", false)
	t.Cleanup(func() { apiClient = previous })
}

func TestRunsCancelStopsRun(t *testing.T) {
	var path string
	runsStub(t, 200, `{"run_id":"r1","status":"cancelled","phase":"cancelled","cancelled":true}`, &path)
	out, err := captureStdout(t, func() error { return runsCancelCmd.RunE(runsCancelCmd, []string{"r1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if path != "POST /api/wf/slim/runs/r1/cancel" {
		t.Errorf("request = %q", path)
	}
	if strings.TrimSpace(out) != "Run r1 stopped." {
		t.Errorf("output = %q", out)
	}
}

func TestRunsCancelAlreadyStopped(t *testing.T) {
	runsStub(t, 200, `{"run_id":"r1","status":"cancelled","cancelled":false,"already_cancelled":true}`, nil)
	out, err := captureStdout(t, func() error { return runsCancelCmd.RunE(runsCancelCmd, []string{"r1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "Run r1 was already stopped." {
		t.Errorf("output = %q", out)
	}
}

func TestRunsCancelFinishedRun(t *testing.T) {
	runsStub(t, 409, `{"error":"run already finished and cannot be cancelled","code":"CONFLICT","run_id":"r1","status":"error","phase":"failed"}`, nil)
	err := runsCancelCmd.RunE(runsCancelCmd, []string{"r1"})
	if err == nil || err.Error() != "run r1 has already failed; there is nothing to stop" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunsCancelUnknownRun(t *testing.T) {
	runsStub(t, 404, `{"error":"run not found","code":"NOT_FOUND"}`, nil)
	err := runsCancelCmd.RunE(runsCancelCmd, []string{"nope"})
	if err == nil || err.Error() != "run nope not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunsListShowsStatus(t *testing.T) {
	runsStub(t, 200, `{"runs":[{"id":"r1","workflow":"wf/HEAD","timestamp":1795518094,"status":"","phase":"running"},{"id":"r0","workflow":"wf/HEAD","timestamp":1795518000}]}`, nil)
	out, err := captureStdout(t, func() error { return runsListCmd.RunE(runsListCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "STATUS") || !strings.Contains(out, "running") {
		t.Errorf("STATUS column missing:\n%s", out)
	}
}
