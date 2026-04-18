package run

import "github.com/spf13/cobra"

// Register adds the `dibbla run` command to the root command.
func Register(root *cobra.Command) {
	root.AddCommand(runCmd)
}
