package deploy

import (
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

// completeMCPAnswer finishes the MCP part of a deploy result before it is
// rendered (DIB-1225): the address of every published server, and the one
// guarantee the server cannot give on its own — a manifest with an `mcp:`
// line never gets a silent deploy. A deploy-api from before the answer
// fields, or one that dropped them, leaves published and notice both empty;
// the CLI then says so, and what to do, instead of printing a success that
// reads as if the line had not been there.
//
// published names the manifest's `mcp:` servers; addr may be nil.
func completeMCPAnswer(res *render.DeployResult, published []string, addr func(string) string) {
	if res == nil {
		return
	}
	if addr != nil && len(res.MCPPublished) > 0 {
		res.MCPAddresses = map[string]string{}
		for _, server := range res.MCPPublished {
			if a := addr(server); a != "" {
				res.MCPAddresses[server] = a
			}
		}
	}
	if len(published) == 0 || len(res.MCPPublished) > 0 || res.MCPNotice != "" {
		return
	}
	res.MCPNotice = "dibbla.yaml publishes " + strings.Join(published, ", ") +
		" as an MCP, but the platform did not say whether it was published (its deploy-api may predate this). " +
		"Check the address with `dibbla mcp server " + published[0] + " --check`, or deploy again."
}
