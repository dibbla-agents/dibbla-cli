// Package mcp implements the `dibbla mcp` command group: helpers for
// connecting MCP clients (Claude Code, Codex CLI, Gemini CLI) to Dibbla's
// hosted MCP endpoints on mcp.<domain>.
//
// The first toolset is the community one: mcp.dibbla.com/community exposes
// community.dibbla.com to coding agents, acting as the calling Dibbla user.
//
//	dibbla mcp community                       print config for all three clients
//	dibbla mcp community --client claude       print just one client's form
//
// The platform toolset (P-0035) is OAuth-protected; `dibbla mcp platform`
// prints its config, --login authorizes this machine, --check proves the chain.
package mcp

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Connect MCP clients (Claude Code, Codex CLI, Gemini CLI) to Dibbla's hosted MCP endpoints",
	Long: `Dibbla hosts MCP endpoints on mcp.<domain>, one toolset per URL path.
The server acts against the platform as you, so everything you can see is
everything the agent can see — nothing more. Authentication differs per
toolset: the community toolset takes your ordinary Dibbla API token as a
bearer header; the platform toolset is protected by OAuth, which the MCP
client negotiates itself (or 'dibbla mcp platform --login' does for this
machine).

Subcommands:
  dibbla mcp community    print client config for the community toolset
                          (community.dibbla.com over MCP)
  dibbla mcp platform     print client config for the platform toolset,
                          --login to authorize, --check to verify`,
}

// Register attaches `dibbla mcp` to root.
func Register(root *cobra.Command) {
	mcpCmd.AddCommand(communityCmd)
	mcpCmd.AddCommand(platformCmd)
	root.AddCommand(mcpCmd)
}

// resolveResult captures the MCP base URL plus where it came from, so the
// output can show a useful "source" hint without re-deriving.
type resolveResult struct {
	URL    string // MCP base URL, no trailing slash; "" on failure
	Source string // human-readable explanation
}

// resolveMCPURL picks the MCP base URL with this precedence:
//
//  1. DIBBLA_MCP_URL env (explicit override, mirrors DIBBLA_AI_GATEWAY_URL).
//  2. Derived from the active Dibbla API URL: replace the leading host
//     label "api." with "mcp.", drop path/query, keep scheme + port.
func resolveMCPURL() resolveResult {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_MCP_URL")); v != "" {
		return resolveResult{
			URL:    strings.TrimRight(v, "/"),
			Source: "env (DIBBLA_MCP_URL)",
		}
	}

	apiURL := config.Load().APIURL
	derived, err := deriveFromAPIURL(apiURL)
	if err != nil {
		return resolveResult{
			URL:    "",
			Source: fmt.Sprintf("could not derive from API URL %q: %v", apiURL, err),
		}
	}
	return resolveResult{
		URL:    derived,
		Source: fmt.Sprintf("derived from API URL %s (api. → mcp.)", apiURL),
	}
}

// deriveFromAPIURL implements the api.X → mcp.X host-label rewrite, the same
// convention `dibbla ai` uses for the AI gateway: hosted MCP lives next to
// the API on the same parent domain. Anything that doesn't match is rejected
// so the caller can show a clear "set DIBBLA_MCP_URL" hint instead of
// silently producing a wrong URL.
func deriveFromAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("host %q does not start with \"api.\"", host)
	}
	newHost := "mcp." + strings.TrimPrefix(host, "api.")
	if port != "" {
		newHost = newHost + ":" + port
	}
	return u.Scheme + "://" + newHost, nil
}
