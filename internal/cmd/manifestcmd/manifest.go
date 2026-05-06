// Package manifestcmd implements `dibbla manifest …` subcommands.
//
// Today this hosts only `validate`, a local schema check against
// internal/manifest/. Server-authoritative checks (env-aware resolution,
// quotas, multiple-public detection, registry allowlist) live in `dibbla
// preview`, which uploads the archive and asks deploy-api.
package manifestcmd

import "github.com/spf13/cobra"

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Work with dibbla.yaml manifests",
	Long: `Work with dibbla.yaml manifests.

Subcommands:
  validate    Local schema check (no server roundtrip)

For server-authoritative validation (env-aware resolution, quota check, full
schema), use 'dibbla preview' instead.`,
}

// Register attaches the manifest command group to the given root.
func Register(root *cobra.Command) {
	manifestCmd.AddCommand(validateCmd)
	root.AddCommand(manifestCmd)
}
