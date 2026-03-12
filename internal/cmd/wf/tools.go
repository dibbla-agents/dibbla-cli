package wf

import (
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Manage agent node tools",
}

var toolsAddCmd = &cobra.Command{
	Use:   "add <workflow> <agent> <tool>",
	Short: "Add a tool to an agent node",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		ops := []map[string]interface{}{
			{"op": "add_tool", "agent": args[1], "tool": args[2]},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Added tool %s to agent %s in %s", args[2], args[1], args[0])
		return printResult(result, "workflow")
	},
}

var toolsRemoveCmd = &cobra.Command{
	Use:   "remove <workflow> <agent> <tool>",
	Short: "Remove a tool from an agent node",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		ops := []map[string]interface{}{
			{"op": "remove_tool", "agent": args[1], "tool": args[2]},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Removed tool %s from agent %s in %s", args[2], args[1], args[0])
		return printResult(result, "workflow")
	},
}

func init() {
	toolsCmd.AddCommand(toolsAddCmd)
	toolsCmd.AddCommand(toolsRemoveCmd)
}
