package deploy

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// DIB-1225, end to end inside the CLI: a project whose dibbla.yaml carries an
// mcp: line, a server that streams the deploy, and the real renderers. The
// output says published-with-address, withdrawn, or — when the server says
// nothing about the line — a notice; never nothing.
func TestRunStream_MCPAnswer(t *testing.T) {
	manifest := "version: 1\nservices:\n  tools:\n    build: .\n    port: 8080\n    public: true\n    mcp: my-tools\n"
	serve := func(result *render.DeployResult) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("\n"))
			helperWriteEvent(w, render.DeployEvent{Type: "deploy", State: "started"})
			helperWriteEvent(w, render.DeployEvent{Type: "result", Result: result})
		}
	}
	run := func(t *testing.T, result *render.DeployResult) (string, string) {
		t.Helper()
		srv, dir := newDibblaTestServer(t, serve(result))
		if err := os.WriteFile(filepath.Join(dir, "dibbla.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		var tty, quiet bytes.Buffer
		opts := Options{APIURL: srv.URL, APIToken: "tok", Path: dir, Alias: "x",
			MCPAddress: func(s string) string { return "https://mcp.example.com/platform/servers/" + s }}
		for _, r := range []render.Renderer{render.NewTTY(&tty, false), render.NewQuiet(&quiet)} {
			if _, err := Run(opts, r); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if code := r.OnDone(); code != 0 {
				t.Fatalf("exit %d", code)
			}
		}
		return tty.String(), quiet.String()
	}
	deployment := render.ResultDeployment{ID: "dep_1", Alias: "x", URL: "https://x.dibbla.com", Status: "running"}

	t.Run("published, with the address", func(t *testing.T) {
		tty, quiet := run(t, &render.DeployResult{Status: "success", Deployment: deployment, MCPPublished: []string{"my-tools"}})
		for _, out := range []string{tty, quiet} {
			for _, want := range []string{"published as MCP: my-tools", "https://mcp.example.com/platform/servers/my-tools", "dibbla mcp server my-tools"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		}
	})
	t.Run("the server's notice", func(t *testing.T) {
		tty, _ := run(t, &render.DeployResult{Status: "success", Deployment: deployment, MCPNotice: "dibbla.yaml publishes my-tools as an MCP, but per-server MCP addresses are switched off on this installation."})
		if !strings.Contains(tty, "switched off") || strings.Contains(tty, "published as MCP") {
			t.Errorf("output:\n%s", tty)
		}
	})
	t.Run("withdrawn", func(t *testing.T) {
		tty, _ := run(t, &render.DeployResult{Status: "success", Deployment: deployment, MCPWithdrawn: []string{"old-tools"}})
		if !strings.Contains(tty, "withdrawn as MCP: old-tools") {
			t.Errorf("output:\n%s", tty)
		}
	})
	t.Run("a server that says nothing about the line is still not silence", func(t *testing.T) {
		tty, quiet := run(t, &render.DeployResult{Status: "success", Deployment: deployment})
		for _, out := range []string{tty, quiet} {
			if !strings.Contains(out, "my-tools") || !strings.Contains(out, "did not say") || !strings.Contains(out, "dibbla mcp server my-tools --check") {
				t.Errorf("output:\n%s", out)
			}
		}
	})
}
