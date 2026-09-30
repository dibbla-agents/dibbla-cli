package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// DIB-1225. A deploy whose manifest publishes a tool server (`mcp:`) says so,
// with the command that connects a client: the deploy output is the one place
// a developer is sure to read, and the server sent mcp_published and
// mcp_notice before the CLI had a field for either.
func TestRenderersNameThePublishedMCP(t *testing.T) {
	ev := DeployEvent{Type: "result", Result: &DeployResult{
		Status:       "success",
		Deployment:   ResultDeployment{ID: "dep_1", Alias: "my-tools", URL: "https://my-tools.dibbla.com", Status: "running"},
		MCPPublished: []string{"my-tools"},
		MCPNotice:    `"other" was not published as an MCP: another app already owns that tool server name.`,
	}}

	renderers := map[string]func(out, errW *bytes.Buffer) Renderer{
		"tty":   func(out, _ *bytes.Buffer) Renderer { return NewTTY(out, false) },
		"log":   func(out, errW *bytes.Buffer) Renderer { return NewLog(out, errW) },
		"quiet": func(out, _ *bytes.Buffer) Renderer { return NewQuiet(out) },
	}
	for name, mk := range renderers {
		t.Run(name, func(t *testing.T) {
			var out, errW bytes.Buffer
			r := mk(&out, &errW)
			r.OnEvent(ev)
			if code := r.OnDone(); code != 0 {
				t.Fatalf("exit %d: an unpublished name is a notice on a success", code)
			}
			combined := out.String() + errW.String()
			for _, want := range []string{"dibbla mcp server my-tools", "another app already owns"} {
				if !strings.Contains(combined, want) {
					t.Errorf("output lacks %q:\n%s", want, combined)
				}
			}
		})
	}

	var out bytes.Buffer
	j := NewJSON(&out)
	j.OnEvent(ev)
	j.OnDone()
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out.String())
	}
	if names, _ := got["mcp_published"].([]any); len(names) != 1 || names[0] != "my-tools" {
		t.Errorf("mcp_published = %v", got["mcp_published"])
	}
	if s, _ := got["mcp_notice"].(string); !strings.Contains(s, "another app") {
		t.Errorf("mcp_notice = %v", got["mcp_notice"])
	}
}
