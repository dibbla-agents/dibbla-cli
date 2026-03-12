package wf

import (
	"fmt"

	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var edgesCmd = &cobra.Command{
	Use:   "edges",
	Short: "Manage workflow edges",
}

var edgesAddCmd = &cobra.Command{
	Use:   `add <workflow> "<src.port -> tgt.port>"`,
	Short: "Add an edge to a workflow",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ops := []map[string]interface{}{
			{"op": "add_edge", "edge": args[1]},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Added edge to %s", args[0])
		return printResult(result, "workflow")
	},
}

var edgesRemoveCmd = &cobra.Command{
	Use:   `remove <workflow> "<src.port -> tgt.port>"`,
	Short: "Remove an edge from a workflow",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ops := []map[string]interface{}{
			{"op": "remove_edge", "edge": args[1]},
		}
		result, err := patchWorkflow(args[0], ops)
		if err != nil {
			return err
		}
		output.Stderr("Removed edge from %s", args[0])
		return printResult(result, "workflow")
	},
}

var edgesListCmd = &cobra.Command{
	Use:   "list <workflow>",
	Short: "List edges of a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/workflows/" + args[0] + "?format=json")
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		edges, _ := result["edges"].([]interface{})
		if flagOutput == "json" {
			return output.PrintJSON(map[string]interface{}{"edges": edges})
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(map[string]interface{}{"edges": edges})
		}
		headers := []string{"EDGE"}
		var rows [][]string
		for _, e := range edges {
			rows = append(rows, []string{fmt.Sprintf("%v", e)})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

func init() {
	edgesCmd.AddCommand(edgesAddCmd)
	edgesCmd.AddCommand(edgesRemoveCmd)
	edgesCmd.AddCommand(edgesListCmd)
}
