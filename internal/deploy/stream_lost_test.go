package deploy

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// A stream that ends before the deploy does is not a failed deploy
// (DIB-1616): the server goes on holding the app's lock. The error says so,
// names the deploy and the two commands that tell more or end it.
func TestRunStream_ConnectionLostIsNotAFailure(t *testing.T) {
	srv, dir := newDibblaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n"))
		helperWriteEvent(w, render.DeployEvent{Type: "deploy", State: "started", Source: "deployment_id=dep_abc"})
		// Keepalive blank lines, then the connection goes — no terminal event.
		_, _ = w.Write([]byte("\n\n"))
		helperWriteEvent(w, render.DeployEvent{Type: "rollout", State: "rollout-start", Source: "0/1 pods ready"})
	})

	fr := &fakeRenderer{}
	_, err := Run(Options{APIURL: srv.URL, APIToken: "stub", Path: dir, Alias: "test-app"}, fr)
	var lost *StreamLostError
	if !errors.As(err, &lost) {
		t.Fatalf("err = %v (%T), want *StreamLostError", err, err)
	}
	if lost.DeploymentID != "dep_abc" || lost.Alias != "test-app" {
		t.Errorf("lost = %+v", lost)
	}
	msg := lost.Message()
	for _, want := range []string{"dep_abc", "not a deploy failure", "dibbla deploy cancel test-app", "dibbla apps get test-app"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	for _, ev := range fr.events {
		if ev.Type == "error" {
			t.Errorf("the stream invented an error event: %+v", ev)
		}
	}
}
