// Package admincmd implements `dibbla admin …`, platform-admin commands
// gated by DIBBLA_ADMIN_TOKEN. The user's API token is NOT used; admin
// endpoints require a separate static token that the platform operator
// configures server-side.
package admincmd

import "github.com/spf13/cobra"

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Platform-admin commands (gated by DIBBLA_ADMIN_TOKEN)",
	Long: `Platform-admin commands. Each subcommand requires DIBBLA_ADMIN_TOKEN in the
environment; the user's normal API token is not used.

Subcommands:
  reconcile    Force one orphan-resource sweep on the deploy-api instance`,
}

// Register attaches the admin command group to the given root.
func Register(root *cobra.Command) {
	adminCmd.AddCommand(reconcileCmd)
	root.AddCommand(adminCmd)
}
