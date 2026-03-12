package wf

import (
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var inputsCmd = &cobra.Command{
	Use:   "inputs",
	Short: "Manage workflow node inputs",
}

var inputsSetCmd = &cobra.Command{
	Use:   "set <workflow> <node> <input> <value>",
	Short: "Set a node input value",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		var value interface{} = args[3]
		isNull, _ := cmd.Flags().GetBool("null")
		if isNull {
			value = nil
		}
		ops := []map[string]interface{}{
			{"op": "update_input", "node": args[1], "input": args[2], "value": value},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Updated input %s.%s on %s", args[1], args[2], args[0])
		return printResult(result, "workflow")
	},
}

func init() {
	inputsSetCmd.Flags().Bool("null", false, "Set value to null")
	inputsCmd.AddCommand(inputsSetCmd)
}
