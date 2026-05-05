package initcmd

import "github.com/spf13/cobra"

// Register adds the `dibbla init` command to root.
func Register(root *cobra.Command) {
	root.AddCommand(initCmd)
}
