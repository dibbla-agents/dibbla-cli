package deploy

import (
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

func TestCompleteMCPAnswer(t *testing.T) {
	addr := func(s string) string { return "https://mcp.example.com/platform/servers/" + s }

	t.Run("a published server gets its address", func(t *testing.T) {
		res := &render.DeployResult{MCPPublished: []string{"my-tools"}}
		completeMCPAnswer(res, []string{"my-tools"}, addr)
		if res.MCPAddresses["my-tools"] != "https://mcp.example.com/platform/servers/my-tools" || res.MCPNotice != "" {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("a manifest with an mcp: line and a server that said nothing is a notice", func(t *testing.T) {
		res := &render.DeployResult{}
		completeMCPAnswer(res, []string{"my-tools"}, addr)
		if !strings.Contains(res.MCPNotice, "my-tools") || !strings.Contains(res.MCPNotice, "did not say") || !strings.Contains(res.MCPNotice, "dibbla mcp server my-tools --check") {
			t.Fatalf("notice = %q", res.MCPNotice)
		}
	})
	t.Run("the server's own notice is kept", func(t *testing.T) {
		res := &render.DeployResult{MCPNotice: "taken"}
		completeMCPAnswer(res, []string{"my-tools"}, addr)
		if res.MCPNotice != "taken" {
			t.Fatalf("notice = %q", res.MCPNotice)
		}
	})
	t.Run("no mcp: line, nothing added", func(t *testing.T) {
		res := &render.DeployResult{}
		completeMCPAnswer(res, nil, addr)
		if res.MCPNotice != "" || res.MCPAddresses != nil {
			t.Fatalf("res = %+v", res)
		}
		completeMCPAnswer(nil, []string{"x"}, addr)
	})
	t.Run("no address function, no addresses", func(t *testing.T) {
		res := &render.DeployResult{MCPPublished: []string{"my-tools"}}
		completeMCPAnswer(res, []string{"my-tools"}, nil)
		if res.MCPAddresses != nil {
			t.Fatalf("res = %+v", res)
		}
	})
}
