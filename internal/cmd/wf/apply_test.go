package wf

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// `wf apply` exists so create-or-update is one command in the CLI too, the way
// platform_workflow_apply is one call for an agent (DIB-674). What is worth
// pinning is the part a caller cannot see from the output: which request it
// actually sent, that the path name won over the one in the file, and that an
// update still carried the If-Match the concurrency check depends on.

type applyStub struct {
	mu       sync.Mutex
	requests []string
	exists   bool
	bodies   []map[string]interface{}
	ifMatch  []string
}

func (s *applyStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			if !s.exists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Workflow not found"}`))
				return
			}
			w.Header().Set("ETag", "etag-1")
			_, _ = w.Write([]byte(`{"name":"digest","nodes":[]}`))
		case r.Method == http.MethodPost, r.Method == http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(raw, &body)
			s.bodies = append(s.bodies, body)
			s.ifMatch = append(s.ifMatch, r.Header.Get("If-Match"))
			status := `"created"`
			if r.Method == http.MethodPut {
				status = `"updated"`
			}
			_, _ = w.Write([]byte(`{"name":"digest","revision":"HEAD","status":` + status + `,"warnings":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runApply(t *testing.T, stub *applyStub, definition string, args ...string) error {
	t.Helper()
	srv := stub.server(t)
	previous := apiClient
	apiClient = apiclient.NewClient(srv.URL, "test-token", false)
	t.Cleanup(func() { apiClient = previous })

	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(definition), 0o600); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	if err := workflowsApplyCmd.Flags().Set("file", path); err != nil {
		t.Fatalf("set --file: %v", err)
	}
	t.Cleanup(func() {
		_ = workflowsApplyCmd.Flags().Set("file", "")
		_ = workflowsApplyCmd.Flags().Set("force", "false")
	})
	for _, flag := range args {
		if err := workflowsApplyCmd.Flags().Set(flag, "true"); err != nil {
			t.Fatalf("set --%s: %v", flag, err)
		}
	}
	return workflowsApplyCmd.RunE(workflowsApplyCmd, []string{"digest"})
}

const applyDefinition = `name: whatever-the-file-says
label: Daily digest
nodes:
  - id: start
    type: api
`

func TestApplyCreatesWhenTheWorkflowIsAbsent(t *testing.T) {
	stub := &applyStub{}
	if err := runApply(t, stub, applyDefinition); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); !strings.Contains(got, "POST /api/wf/slim/workflows") {
		t.Fatalf("requests = %s; want a create", got)
	}
	if len(stub.bodies) != 1 || stub.bodies[0]["name"] != "digest" {
		t.Fatalf("the name in the file won over the one in the path: %v", stub.bodies)
	}
}

func TestApplyUpdatesWithIfMatchWhenTheWorkflowExists(t *testing.T) {
	stub := &applyStub{exists: true}
	if err := runApply(t, stub, applyDefinition); err != nil {
		t.Fatalf("apply: %v", err)
	}
	joined := strings.Join(stub.requests, ", ")
	if !strings.Contains(joined, "PUT /api/wf/slim/workflows/digest") {
		t.Fatalf("requests = %s; want an update", joined)
	}
	if strings.Contains(joined, "POST /api/wf/slim/workflows,") {
		t.Fatalf("an existing workflow was also created: %s", joined)
	}
	if len(stub.ifMatch) != 1 || stub.ifMatch[0] != "etag-1" {
		t.Fatalf("If-Match = %v; the concurrency check was dropped", stub.ifMatch)
	}
}

func TestApplyForceDropsTheConcurrencyCheck(t *testing.T) {
	stub := &applyStub{exists: true}
	if err := runApply(t, stub, applyDefinition, "force"); err != nil {
		t.Fatalf("apply --force: %v", err)
	}
	if len(stub.ifMatch) != 1 || stub.ifMatch[0] != "" {
		t.Fatalf("If-Match = %v; --force must overwrite unconditionally", stub.ifMatch)
	}
}

func TestApplyNeedsAFile(t *testing.T) {
	previous := apiClient
	t.Cleanup(func() { apiClient = previous })
	_ = workflowsApplyCmd.Flags().Set("file", "")
	if err := workflowsApplyCmd.RunE(workflowsApplyCmd, []string{"digest"}); err == nil {
		t.Fatal("apply without --file succeeded")
	}
}
