package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// A deploy that fails before the server stream produces a terminal event
// (here: local manifest validation) must still render the error and exit
// non-zero. Regression test for the silent exit-0 multi-service deploy.
func TestRunWithRendererPreStreamFailure(t *testing.T) {
	dir := t.TempDir()
	manifest := "services:\n  web:\n    build: .\n" // no version: → locally invalid
	if err := os.WriteFile(filepath.Join(dir, "dibbla.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := runWithRenderer(deploypkg.Options{Path: dir}, render.NewQuiet(&out))

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got 0; output: %q", out.String())
	}
	if !strings.Contains(out.String(), "CLI_ERROR") || !strings.Contains(out.String(), "manifest") {
		t.Fatalf("expected rendered manifest error, got: %q", out.String())
	}
}

// terminalTracking must flag terminal events (result/error) and pass
// everything through, so runWithRenderer doesn't double-render an error
// the stream already showed.
func TestTerminalTracking(t *testing.T) {
	var out bytes.Buffer
	tr := &terminalTracking{Renderer: render.NewQuiet(&out)}

	tr.OnEvent(render.DeployEvent{Type: "build", State: "running"})
	if tr.sawTerminal {
		t.Fatal("build event must not count as terminal")
	}
	tr.OnEvent(render.DeployEvent{Type: "error", Error: &render.DeployError{
		APIError: &render.APIError{Code: "BUILD_FAILED", Message: "boom"},
	}})
	if !tr.sawTerminal {
		t.Fatal("error event must count as terminal")
	}
}
