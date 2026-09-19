package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// operationServer serves one operation that is running on the first status
// read and terminal on the next, with events and a build log that arrive in
// between — the shape of a push-started deploy being followed.
func operationServer(t *testing.T, terminal map[string]any) (*httptest.Server, *int32) {
	t.Helper()
	var reads int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/deploy/deployment-operations/deployment:op-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n := atomic.AddInt32(&reads, 1)
		doc := map[string]any{"operation_id": "deployment:op-1", "kind": "git_push", "alias": "shop", "phase": "running", "terminal": false,
			"created_at": "2026-09-19T10:00:00Z", "source": map[string]any{"repo": "acme/shop.git", "ref": "refs/heads/main", "commit_sha": strings.Repeat("a", 40)}}
		if n > 1 {
			for k, v := range terminal {
				doc[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("GET /api/deploy/deployment-operations/deployment:op-1/events", func(w http.ResponseWriter, r *http.Request) {
		events := []map[string]any{}
		if r.URL.Query().Get("after") == "0" {
			events = append(events, map[string]any{"sequence": 1, "timestamp": "2026-09-19T10:00:01Z", "kind": "progress", "level": "info", "text": "build · running · FROM node (1/3)"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": events, "has_more": false})
	})
	mux.HandleFunc("GET /api/deploy/deployment-operations/deployment:op-1/logs", func(w http.ResponseWriter, r *http.Request) {
		lines := []map[string]any{}
		if r.URL.Query().Get("after") == "0" {
			lines = append(lines, map[string]any{"sequence": 1, "timestamp": "2026-09-19T10:00:02Z", "level": "info", "line": "#3 [1/3] FROM node:20"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"lines": lines, "has_more": false})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"OPERATION_NOT_FOUND","message":"No such operation within your organization."}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &reads
}

func TestDeployStatusFollowRendersTheLedgerAndExitsWithTheDeploysCode(t *testing.T) {
	srv, reads := operationServer(t, map[string]any{"phase": "succeeded", "terminal": true, "finished_at": "2026-09-19T10:01:00Z",
		"result": map[string]any{"result": "deployed", "alias": "shop", "url": "https://shop.example", "deployment_id": "dep_1", "status": "running", "commit_sha": strings.Repeat("a", 40)}})
	var stdout, stderr bytes.Buffer
	code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", true, false, render.NewLog(&stdout, &stderr), time.Millisecond)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"shop ← " + strings.Repeat("a", 40), "build · running · FROM node (1/3)", "#3 [1/3] FROM node:20", "status=ok url=https://shop.example alias=shop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if atomic.LoadInt32(reads) < 2 {
		t.Fatalf("status read %d times; --follow must poll until terminal", *reads)
	}
	// Each ledger line is rendered once, however many polls it took.
	if strings.Count(out, "FROM node:20") != 1 {
		t.Errorf("log line rendered %d times:\n%s", strings.Count(out, "FROM node:20"), out)
	}
}

func TestDeployStatusFollowFailsLikeADeploy(t *testing.T) {
	srv, _ := operationServer(t, map[string]any{"phase": "failed", "terminal": true, "failure": map[string]any{"code": "BUILD_FAILED", "summary": "Dockerfile not found in archive root"}})
	var stdout, stderr bytes.Buffer
	code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", true, false, render.NewLog(&stdout, &stderr), time.Millisecond)
	if code == 0 {
		t.Fatalf("failed deploy exited 0:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `code=BUILD_FAILED msg="Dockerfile not found in archive root"`) {
		t.Errorf("failure not rendered:\n%s", stdout.String())
	}
}

func TestDeployStatusWithoutFollowPrintsThePhaseAndExitsZero(t *testing.T) {
	srv, _ := operationServer(t, nil)
	var stdout, stderr bytes.Buffer
	code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", false, false, nil, time.Millisecond)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"deployment:op-1 — shop (running)", "Commit:  " + strings.Repeat("a", 40), "dibbla deploy status deployment:op-1 --follow"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}
	// --json emits the server document verbatim.
	stdout.Reset()
	if code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", false, true, nil, time.Millisecond); code != 0 {
		t.Fatal(code)
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc["operation_id"] != "deployment:op-1" {
		t.Fatalf("--json: %v %s", err, stdout.String())
	}
}

func TestDeployStatusUnknownOperationIsNotFound(t *testing.T) {
	srv, _ := operationServer(t, nil)
	var stdout, stderr bytes.Buffer
	code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:nope", false, false, nil, time.Millisecond)
	if code != 4 || !strings.Contains(stderr.String(), "OPERATION_NOT_FOUND") || !strings.Contains(stderr.String(), "hint:") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
}
