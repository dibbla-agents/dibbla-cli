package wf

import (
	"fmt"

	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var revisionsCmd = &cobra.Command{
	Use:     "revisions",
	Aliases: []string{"rev"},
	Short:   "Manage workflow revisions",
}

var revisionsListCmd = &cobra.Command{
	Use:   "list <workflow>",
	Short: "List revisions of a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/slim/workflows/" + args[0] + "/revisions?format=json")
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		revisions, _ := result["revisions"].([]interface{})
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		headers := []string{"ID", "TIMESTAMP", "LABEL"}
		var rows [][]string
		for _, r := range revisions {
			rev := r.(map[string]interface{})
			id := fmt.Sprintf("%v", rev["id"])
			ts := fmt.Sprintf("%v", rev["timestamp"])
			label := fmt.Sprintf("%v", rev["label"])
			rows = append(rows, []string{id, ts, label})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var revisionsCreateCmd = &cobra.Command{
	Use:   "create <workflow>",
	Short: "Create a revision snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Post("/api/wf/slim/workflows/"+args[0]+"/revisions?format=json", nil)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Created revision %v for %v", result["revision"], result["workflow"])
		if flagQuiet {
			fmt.Println(result["revision"])
		} else if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		return nil
	},
}

var revisionsRestoreCmd = &cobra.Command{
	Use:   "restore <workflow> <revision_id>",
	Short: "Restore a revision to HEAD",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Post("/api/wf/slim/workflows/"+args[0]+"/revisions/"+args[1]+"/restore?format=json", nil)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Restored revision %v for %v", result["revision"], result["workflow"])
		return nil
	},
}

func init() {
	revisionsCmd.AddCommand(revisionsListCmd)
	revisionsCmd.AddCommand(revisionsCreateCmd)
	revisionsCmd.AddCommand(revisionsRestoreCmd)
}
