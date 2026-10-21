// Package admincmd implements `dibbla admin …`, platform-admin commands.
// reconcile is gated by DIBBLA_ADMIN_TOKEN, a static operator token; models
// and costs use the logged-in user's token and are allowed for global
// admins only.
package admincmd

import "github.com/spf13/cobra"

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Platform-admin commands",
	Long: `Platform-admin commands.

Subcommands:
  costs        What every customer's AI calls cost Dibbla in a month (global admin login)
  models       Manage the platform model catalog (global admin login)
  reconcile    Force one orphan-resource sweep on the deploy-api instance
               (requires DIBBLA_ADMIN_TOKEN; the user's API token is not used)`,
}

// Register attaches the admin command group to the given root.
func Register(root *cobra.Command) {
	adminCmd.AddCommand(reconcileCmd)
	adminCmd.AddCommand(modelsCmd)
	adminCmd.AddCommand(costsCmd)
	root.AddCommand(adminCmd)
}
