package update

import "github.com/spf13/cobra"

// Register adds the `dibbla update` command to root.
//
// currentVersion should be the build-time `cmd.Version` so the command
// can report drift and decide whether to allow self-replace.
func Register(root *cobra.Command, currentVersion string) {
	version = currentVersion
	root.AddCommand(updateCmd)
}
