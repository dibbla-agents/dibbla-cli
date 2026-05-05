package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// fakeRenderer captures every event the streaming client decodes so the
// test can assert on the sequence rather than relying on rendered output.
type fakeRenderer struct {
	events []render.DeployEvent
	exit   int
}

func (f *fakeRenderer) OnEvent(ev render.DeployEvent) { f.events = append(f.events, ev) }
func (f *fakeRenderer) OnDone() int                   { return f.exit }

// helperWriteEvent is the test-side mirror of the server's eventEmitter:
// one JSON object per line, flushed immediately. The server's primer
// newline is also reproduced so the client's bufio.Scanner sees the same
// shape it would on a real connection.
func helperWriteEvent(w http.ResponseWriter, ev render.DeployEvent) {
	_ = json.NewEncoder(w).Encode(ev)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func newDibblaTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srv, dir
}

func TestRunStream_HappyPath(t *testing.T) {
	srv, dir := newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/x-ndjson") {
			t.Errorf("Accept header = %q, want application/x-ndjson", got)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		helperWriteEvent(w, render.DeployEvent{Type: "deploy", State: "started"})
		helperWriteEvent(w, render.DeployEvent{Type: "build", State: "done", Step: "snapshot-source", StepIndex: 1, StepCount: 1, ElapsedMs: 200})
		helperWriteEvent(w, render.DeployEvent{
			Type: "result",
			Result: &render.DeployResult{
				Status: "success",
				Deployment: render.ResultDeployment{
					ID:     "dep_xyz",
					Alias:  "test-app",
					URL:    "https://test-app.dibbla.com",
					Status: "running",
				},
			},
		})
	})

	fr := &fakeRenderer{}
	resp, err := Run(Options{
		APIURL:   srv.URL,
		APIToken: "stub",
		Path:     dir,
		Alias:    "test-app",
	}, fr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp == nil || resp.Deployment.URL != "https://test-app.dibbla.com" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(fr.events) != 3 {
		t.Errorf("expected 3 events, got %d: %+v", len(fr.events), fr.events)
	}
	if fr.events[len(fr.events)-1].Type != "result" {
		t.Errorf("expected terminal event type=result, got %q", fr.events[len(fr.events)-1].Type)
	}
}

func TestRunStream_BuildFailure(t *testing.T) {
	srv, dir := newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		helperWriteEvent(w, render.DeployEvent{Type: "build", State: "fail", Step: "go-build", StepIndex: 5, StepCount: 8})
		helperWriteEvent(w, render.DeployEvent{
			Type: "error",
			Error: &render.DeployError{
				APIError:   &render.APIError{Code: "BUILD_FAILED", Message: "go build exit=2"},
				FailedStep: "go-build",
				StepIndex:  5,
				StepCount:  8,
				ParsedItems: []render.ParsedBuildError{
					{File: "main.go", Line: 10, Col: 1, Message: "undefined: foo"},
				},
				RetryCmd: "dibbla deploy --update -a test-app",
			},
		})
	})

	fr := &fakeRenderer{exit: 2}
	_, err := Run(Options{
		APIURL:   srv.URL,
		APIToken: "stub",
		Path:     dir,
		Alias:    "test-app",
	}, fr)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "BUILD_FAILED") {
		t.Errorf("expected BUILD_FAILED in error, got: %v", err)
	}
	terminal := fr.events[len(fr.events)-1]
	if terminal.Type != "error" {
		t.Errorf("expected terminal event type=error, got %q", terminal.Type)
	}
	if terminal.Error == nil || terminal.Error.FailedStep != "go-build" {
		t.Errorf("expected FailedStep=go-build, got %+v", terminal.Error)
	}
}

func TestRunStream_LegacyJSON(t *testing.T) {
	// Server doesn't honor streaming — emits a single JSON object as
	// before. The client must fall back to the legacy parse path so
	// older deploy-api binaries keep working.
	srv, dir := newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		body, _ := json.Marshal(map[string]any{
			"status": "success",
			"deployment": map[string]any{
				"id":     "dep_xyz",
				"alias":  "test-app",
				"url":    "https://test-app.dibbla.com",
				"status": "running",
			},
		})
		_, _ = w.Write(body)
	})

	fr := &fakeRenderer{}
	resp, err := Run(Options{
		APIURL:   srv.URL,
		APIToken: "stub",
		Path:     dir,
		Alias:    "test-app",
	}, fr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.Deployment.URL != "https://test-app.dibbla.com" {
		t.Fatalf("unexpected url: %q", resp.Deployment.URL)
	}
	// Legacy single-JSON responses are synthesized into a single
	// terminal event so the renderer can still display the result. Older
	// behavior dropped them on the floor — keep this test as the
	// regression guard.
	if len(fr.events) != 1 || fr.events[0].Type != "result" {
		t.Errorf("expected one synthesized result event on legacy path, got %d: %+v", len(fr.events), fr.events)
	}
	if fr.events[0].Result == nil || fr.events[0].Result.Deployment.URL != "https://test-app.dibbla.com" {
		t.Errorf("synthesized result missing URL, got %+v", fr.events[0].Result)
	}
}

func TestRunStream_VerboseQueryParam(t *testing.T) {
	var seenURL string
	srv, dir := newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		helperWriteEvent(w, render.DeployEvent{
			Type: "result",
			Result: &render.DeployResult{
				Status:     "success",
				Deployment: render.ResultDeployment{Alias: "x", URL: "https://x.dibbla.com"},
			},
		})
	})

	_, err := Run(Options{
		APIURL:       srv.URL,
		APIToken:     "stub",
		Path:         dir,
		Alias:        "x",
		VerboseBuild: true,
	}, &fakeRenderer{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(seenURL, "verbose=1") {
		t.Errorf("expected verbose=1 in request URL, got %q", seenURL)
	}
}

// guard against accidental import cycles when fakeRenderer is referenced
// via Run; if this test file ever fails to compile that's a real signal.
var _ render.Renderer = (*fakeRenderer)(nil)
