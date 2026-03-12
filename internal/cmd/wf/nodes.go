package wf

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Manage workflow nodes",
}

var nodesAddCmd = &cobra.Command{
	Use:   "add <workflow>",
	Short: "Add a node to a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var nodeDef interface{}
		inlineStr, _ := cmd.Flags().GetString("inline")
		filePath, _ := cmd.Flags().GetString("file")
		if inlineStr != "" {
			if err := json.Unmarshal([]byte(inlineStr), &nodeDef); err != nil {
				return fmt.Errorf("parsing --inline JSON: %w", err)
			}
		} else if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			if err := parseFileContent(data, &nodeDef); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("either --file (-f) or --inline is required")
		}
		ops := []map[string]interface{}{
			{"op": "add_node", "node": nodeDef},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Added node to %s", args[0])
		return printResult(result, "workflow")
	},
}

var nodesRemoveCmd = &cobra.Command{
	Use:   "remove <workflow> <node_id>",
	Short: "Remove a node from a workflow",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !confirmAction(fmt.Sprintf("Remove node %q from %q?", args[1], args[0]), yes) {
			return nil
		}
		ops := []map[string]interface{}{
			{"op": "remove_node", "node": args[1]},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Removed node %s from %s", args[1], args[0])
		return printResult(result, "workflow")
	},
}

func init() {
	nodesAddCmd.Flags().StringP("file", "f", "", "Node definition file (YAML/JSON)")
	nodesAddCmd.Flags().String("inline", "", "Inline node definition (JSON)")
	nodesRemoveCmd.Flags().Bool("yes", false, "Skip confirmation")
	nodesCmd.AddCommand(nodesAddCmd)
	nodesCmd.AddCommand(nodesRemoveCmd)
}
