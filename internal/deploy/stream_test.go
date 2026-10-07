package deploy

import (
	"bytes"
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

// envWarningServer answers a deploy the way deploy-api does for a person
// whose dibbla.yaml carries secret-looking environment: entries (DIB-1339):
// streamed as the result event's DeployResponse, or — an older negotiation —
// as the single JSON document. warnings nil is the same deploy without them.
func envWarningServer(t *testing.T, streamed bool, warnings []string) (*httptest.Server, string) {
	return newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		result := render.DeployResult{
			Status:      "success",
			Deployment:  render.ResultDeployment{ID: "dep_1", Alias: "shop", URL: "https://shop.dibbla.com", Status: "running"},
			EnvWarnings: warnings,
		}
		if !streamed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n"))
		helperWriteEvent(w, render.DeployEvent{Type: "result", Result: &result})
	})
}

// The server's env_warnings reach the person on both response paths, in the
// human output and in --json, and a deploy without them prints nothing new.
func TestRun_EnvWarningsAreReported(t *testing.T) {
	warnings := []string{
		"STRIPE_SECRET_KEY has the name of a secret — consider making it a secret (dibbla.yaml environment is readable by anyone who can read the app's source)",
	}
	for _, path := range []struct {
		name     string
		streamed bool
	}{{"stream", true}, {"legacy", false}} {
		t.Run(path.name, func(t *testing.T) {
			deploy := func(warnings []string, r func(*bytes.Buffer) render.Renderer) string {
				srv, dir := envWarningServer(t, path.streamed, warnings)
				var out bytes.Buffer
				rr := r(&out)
				if _, err := Run(Options{APIURL: srv.URL, APIToken: "stub", Path: dir, Alias: "shop"}, rr); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if code := rr.OnDone(); code != 0 {
					t.Fatalf("exit %d — the deploy went ahead", code)
				}
				return out.String()
			}
			quiet := func(b *bytes.Buffer) render.Renderer { return render.NewQuiet(b) }
			asJSON := func(b *bytes.Buffer) render.Renderer { return render.NewJSON(b) }

			got := deploy(warnings, quiet)
			for _, want := range []string{
				"! env: " + warnings[0] + "\n",
				"! env: to move one into secrets: remove its line from dibbla.yaml, run dibbla secrets request NAME -d shop",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("output lacks %q:\n%s", want, got)
				}
			}
			t.Logf("quiet output:\n%s", got)

			var doc map[string]any
			if err := json.Unmarshal([]byte(deploy(warnings, asJSON)), &doc); err != nil {
				t.Fatal(err)
			}
			if list, _ := doc["env_warnings"].([]any); len(list) != 1 || list[0] != warnings[0] {
				t.Errorf("--json env_warnings = %v", doc["env_warnings"])
			}

			if clean := deploy(nil, quiet); strings.Contains(clean, "!") || strings.Count(clean, "\n") != 1 {
				t.Errorf("a deploy without env_warnings must print its one line and nothing else:\n%s", clean)
			}
			doc = nil
			if err := json.Unmarshal([]byte(deploy(nil, asJSON)), &doc); err != nil {
				t.Fatal(err)
			}
			if _, present := doc["env_warnings"]; present {
				t.Errorf("env_warnings present without warnings: %v", doc)
			}
		})
	}
}

// guard against accidental import cycles when fakeRenderer is referenced
// via Run; if this test file ever fails to compile that's a real signal.
var _ render.Renderer = (*fakeRenderer)(nil)
