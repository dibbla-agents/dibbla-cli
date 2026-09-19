package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/gitcred"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// gitCredentialCmd is the git credential helper `dibbla login` registers for
// Dibbla's git host. git runs it; people do not. See internal/gitcred.
var gitCredentialCmd = &cobra.Command{
	Use:    "git-credential <get|store|erase>",
	Short:  "Git credential helper (called by git, not by you)",
	Hidden: true,
	Long: `Answer git's credential protocol for Dibbla's git host from the same login
"dibbla login" stored. Registered in your user-level git config as

  credential.https://<api-host>/git.helper = !<dibbla> git-credential

so git push, git pull and git clone against Dibbla work without a token in
.git/config or ~/.git-credentials. Other remotes are never consulted.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"get", "store", "erase"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return gitcred.Run(args[0], os.Stdin, os.Stdout, os.Stderr)
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(gitCredentialCmd)
}

// setupGitCredentialHelper registers the helper for apiURL and reports a
// warning rather than failing the caller: a login is still a login when git
// is missing, and the helper is registered again on the next login or clone.
func setupGitCredentialHelper(apiURL string) {
	if err := gitcred.Register(apiURL); err != nil {
		fmt.Fprintf(os.Stderr, "%s git credential helper not registered (%v) — git push/pull against Dibbla will prompt for a password.\n", platform.Icon("⚠", "[!]"), err)
	}
}
