package mcp

import (
	"io"

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
	// Runtime failures from --check (bad token, missing env var) are
	// diagnoses, not usage mistakes — don't dump the flag help after them.
	SilenceUsage: true,
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
	endpoint, source, err := communityToolset.endpoint()
	if err != nil {
		return err
	}
	return communityToolset.printConfig(w, client, endpoint, source)
}
