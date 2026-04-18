package template

import "github.com/spf13/cobra"

// Register adds the `dibbla template` command (and its subcommands) to root.
func Register(root *cobra.Command) {
	templateCmd.AddCommand(listCmd)
	templateCmd.AddCommand(installCmd)
	root.AddCommand(templateCmd)
}
