package logs

import "github.com/spf13/cobra"

// Register adds the `dibbla logs` command to root.
func Register(root *cobra.Command) {
	root.AddCommand(logsCmd)
}
