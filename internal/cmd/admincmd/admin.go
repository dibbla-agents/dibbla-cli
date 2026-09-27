// Package admincmd implements `dibbla admin …`, platform-admin commands.
// reconcile is gated by DIBBLA_ADMIN_TOKEN, a static operator token; models
// uses the logged-in user's token and is allowed for global admins only.
package admincmd

import "github.com/spf13/cobra"

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Platform-admin commands",
	Long: `Platform-admin commands.

Subcommands:
  models       Manage the platform model catalog (global admin login)
  reconcile    Force one orphan-resource sweep on the deploy-api instance
               (requires DIBBLA_ADMIN_TOKEN; the user's API token is not used)`,
}

// Register attaches the admin command group to the given root.
func Register(root *cobra.Command) {
	adminCmd.AddCommand(reconcileCmd)
	adminCmd.AddCommand(modelsCmd)
	root.AddCommand(adminCmd)
}
