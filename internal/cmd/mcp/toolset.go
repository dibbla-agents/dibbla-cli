package mcp

import (
	"fmt"
	"io"
	"strings"
)

// toolset describes one hosted MCP toolset well enough for the config printers
// and the check to be written once. P-0035 E5 asks for exactly this: community
// and platform share one implementation instead of each carrying a hard-coded
// printer, so a third toolset is a value, not a file.
type toolset struct {
	// Path is the URL path under mcp.<domain>.
	Path string
	// ServerName is the key the client config registers the server under.
	ServerName string
	// WhoamiTool is the tool the check calls to prove identity.
	WhoamiTool string
	// StaticBearer says the toolset authenticates with the Dibbla API token
	// in an Authorization header (community), as opposed to OAuth the client
	// negotiates itself (platform).
	StaticBearer bool
	// NotFoundHint explains a 404 on the endpoint in this toolset's terms.
	NotFoundHint string
}

var (
	communityToolset = toolset{
		Path:         "/community",
		ServerName:   "dibbla-community",
		WhoamiTool:   "community_whoami",
		StaticBearer: true,
		NotFoundHint: "no community toolset at this endpoint (HTTP 404) — the community is a central-install feature; customer and dev installs serve 404 here by design",
	}
	platformToolset = toolset{
		Path:         "/platform",
		ServerName:   "dibbla-platform",
		WhoamiTool:   "platform_whoami",
		StaticBearer: false,
		NotFoundHint: "no platform toolset at this endpoint (HTTP 404) — /platform is switched off on this install (PLATFORM_MCP_ENABLED); ask the operator, or check DIBBLA_MCP_URL points at the right host",
	}
)

// endpoint resolves the toolset's full URL, or an actionable error.
func (ts toolset) endpoint() (string, resolveResult, error) {
	r := resolveMCPURL()
	if r.URL == "" {
		return "", r, fmt.Errorf("cannot resolve the MCP host: %s\nSet DIBBLA_MCP_URL explicitly, e.g. DIBBLA_MCP_URL=https://mcp.dibbla.com", r.Source)
	}
	return r.URL + ts.Path, r, nil
}

// printConfig emits the client configuration for one client, or all three.
func (ts toolset) printConfig(w io.Writer, client, endpoint string, source resolveResult) error {
	switch strings.ToLower(client) {
	case "claude":
		ts.printClaude(w, endpoint)
	case "codex":
		ts.printCodex(w, endpoint)
	case "gemini":
		ts.printGemini(w, endpoint)
	case "":
		fmt.Fprintf(w, "# MCP endpoint: %s (%s)\n", endpoint, source.Source)
		if ts.StaticBearer {
			fmt.Fprintf(w, "# All three forms reference $DIBBLA_API_TOKEN — export it where the client runs.\n\n")
		} else {
			fmt.Fprintf(w, "# No token in any form: the client runs the OAuth flow itself on first use\n")
			fmt.Fprintf(w, "# (discovery → browser consent → token). Run `dibbla mcp platform --check` to\n")
			fmt.Fprintf(w, "# verify the same chain from this machine.\n\n")
		}
		fmt.Fprintf(w, "## Claude Code\n\n")
		ts.printClaude(w, endpoint)
		fmt.Fprintf(w, "\n## Codex CLI (~/.codex/config.toml)\n\n")
		ts.printCodex(w, endpoint)
		fmt.Fprintf(w, "\n## Gemini CLI (~/.gemini/settings.json)\n\n")
		ts.printGemini(w, endpoint)
	default:
		return fmt.Errorf("unknown --client %q (expected claude, codex, or gemini)", client)
	}
	return nil
}

func (ts toolset) printClaude(w io.Writer, endpoint string) {
	if ts.StaticBearer {
		// Single quotes around the header are load-bearing: with double quotes the
		// shell expands ${DIBBLA_API_TOKEN} before `claude mcp add` sees it, which
		// either bakes the real token into ~/.claude.json in cleartext or — when
		// the variable is unset — silently writes an empty "Bearer " header. The
		// placeholder must survive verbatim so Claude Code expands it at connect
		// time (review 2026-08-19, finding A2).
		fmt.Fprintf(w, `# One-liner:
#   claude mcp add --transport http %s %s \
#     --header 'Authorization: Bearer ${DIBBLA_API_TOKEN}'
# Or as .mcp.json / settings JSON:
{
  "mcpServers": {
    "%s": {
      "type": "http",
      "url": "%s",
      "headers": { "Authorization": "Bearer ${DIBBLA_API_TOKEN}" }
    }
  }
}
`, ts.ServerName, endpoint, ts.ServerName, endpoint)
		return
	}
	// OAuth: no header at all. Claude Code discovers the authorization server
	// from the 401 challenge and runs the browser flow when the server is first
	// used (`/mcp` in the session shows the connection state).
	fmt.Fprintf(w, `# One-liner:
#   claude mcp add --transport http %s %s
# Then authenticate from inside Claude Code: /mcp → %s → Authenticate.
# Or as .mcp.json / settings JSON:
{
  "mcpServers": {
    "%s": {
      "type": "http",
      "url": "%s"
    }
  }
}
`, ts.ServerName, endpoint, ts.ServerName, ts.ServerName, endpoint)
}

func (ts toolset) printCodex(w io.Writer, endpoint string) {
	if ts.StaticBearer {
		fmt.Fprintf(w, `[mcp_servers.%s]
url = "%s"
bearer_token_env_var = "DIBBLA_API_TOKEN"
`, ts.ServerName, endpoint)
		return
	}
	fmt.Fprintf(w, `[mcp_servers.%s]
url = "%s"
# OAuth: no bearer_token_env_var. Authenticate once with
#   codex mcp login %s
`, ts.ServerName, endpoint, ts.ServerName)
}

func (ts toolset) printGemini(w io.Writer, endpoint string) {
	if ts.StaticBearer {
		fmt.Fprintf(w, `{
  "mcpServers": {
    "%s": {
      "httpUrl": "%s",
      "headers": { "Authorization": "Bearer ${DIBBLA_API_TOKEN}" }
    }
  }
}
`, ts.ServerName, endpoint)
		return
	}
	fmt.Fprintf(w, `{
  "mcpServers": {
    "%s": {
      "httpUrl": "%s",
      "oauth": { "enabled": true }
    }
  }
}
`, ts.ServerName, endpoint)
}
