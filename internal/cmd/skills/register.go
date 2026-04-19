package skills

import "github.com/spf13/cobra"

// Register adds the `dibbla skills` command (and its subcommands) to root.
func Register(root *cobra.Command) {
	skillsCmd.AddCommand(listCmd)
	skillsCmd.AddCommand(installCmd)
	root.AddCommand(skillsCmd)
}
