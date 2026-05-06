package uninstall

import "github.com/spf13/cobra"

// Register adds the `dibbla uninstall` command to root.
//
// currentVersion is the build-time `cmd.Version`; when it's "dev" the
// command refuses to remove the binary (treated as MethodGoInstall).
func Register(root *cobra.Command, currentVersion string) {
	version = currentVersion
	root.AddCommand(uninstallCmd)
}
