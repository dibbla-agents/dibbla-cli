package mcp

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var (
	communityClient string
	communityCheck  bool
)

var communityCmd = &cobra.Command{
	Use:   "community",
	Short: "Print MCP client config for the Dibbla community toolset",
	Long: `Prints ready-to-paste configuration connecting an MCP client to the
community toolset — community.dibbla.com over MCP, acting as you.

Every form references the DIBBLA_API_TOKEN environment variable instead of
embedding the token, so nothing secret lands in a config file. Export the
variable in the shell (or environment) the client runs in.

Reads need nothing beyond a community sign-in (visit community.dibbla.com
once with your Dibbla login). Posting additionally requires membership in
the community's write group — ask an admin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if communityCheck {
			return runCommunityCheck(cmd.OutOrStdout())
		}
		return runCommunity(cmd.OutOrStdout(), communityClient)
	},
}

func init() {
	communityCmd.Flags().StringVar(&communityClient, "client", "",
		"emit config for one client: claude, codex, or gemini (default: all three)")
	communityCmd.Flags().BoolVar(&communityCheck, "check", false,
		"verify the connection end to end: resolve the token, initialize, and call community_whoami")
}

func runCommunity(w io.Writer, client string) error {
	r := resolveMCPURL()
	if r.URL == "" {
		return fmt.Errorf("cannot resolve the MCP host: %s\nSet DIBBLA_MCP_URL explicitly, e.g. DIBBLA_MCP_URL=https://mcp.dibbla.com", r.Source)
	}
	endpoint := r.URL + "/community"

	switch strings.ToLower(client) {
	case "claude":
		printClaude(w, endpoint)
	case "codex":
		printCodex(w, endpoint)
	case "gemini":
		printGemini(w, endpoint)
	case "":
		fmt.Fprintf(w, "# MCP endpoint: %s (%s)\n", endpoint, r.Source)
		fmt.Fprintf(w, "# All three forms reference $DIBBLA_API_TOKEN — export it where the client runs.\n\n")
		fmt.Fprintf(w, "## Claude Code\n\n")
		printClaude(w, endpoint)
		fmt.Fprintf(w, "\n## Codex CLI (~/.codex/config.toml)\n\n")
		printCodex(w, endpoint)
		fmt.Fprintf(w, "\n## Gemini CLI (~/.gemini/settings.json)\n\n")
		printGemini(w, endpoint)
	default:
		return fmt.Errorf("unknown --client %q (expected claude, codex, or gemini)", client)
	}
	return nil
}

func printClaude(w io.Writer, endpoint string) {
	// Single quotes around the header are load-bearing: with double quotes the
	// shell expands ${DIBBLA_API_TOKEN} before `claude mcp add` sees it, which
	// either bakes the real token into ~/.claude.json in cleartext or — when
	// the variable is unset — silently writes an empty "Bearer " header. The
	// placeholder must survive verbatim so Claude Code expands it at connect
	// time (review 2026-08-19, finding A2).
	fmt.Fprintf(w, `# One-liner:
#   claude mcp add --transport http dibbla-community %s \
#     --header 'Authorization: Bearer ${DIBBLA_API_TOKEN}'
# Or as .mcp.json / settings JSON:
{
  "mcpServers": {
    "dibbla-community": {
      "type": "http",
      "url": "%s",
      "headers": { "Authorization": "Bearer ${DIBBLA_API_TOKEN}" }
    }
  }
}
`, endpoint, endpoint)
}

func printCodex(w io.Writer, endpoint string) {
	fmt.Fprintf(w, `[mcp_servers.dibbla-community]
url = "%s"
bearer_token_env_var = "DIBBLA_API_TOKEN"
`, endpoint)
}

func printGemini(w io.Writer, endpoint string) {
	fmt.Fprintf(w, `{
  "mcpServers": {
    "dibbla-community": {
      "httpUrl": "%s",
      "headers": { "Authorization": "Bearer ${DIBBLA_API_TOKEN}" }
    }
  }
}
`, endpoint)
}
