package wf

import (
	"fmt"
	"strconv"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Inspect workflow runs",
}

var runsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent workflow runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/slim/runs?format=json"
		if w, _ := cmd.Flags().GetString("workflow"); w != "" {
			path += "&workflow=" + w
		}
		if n, _ := cmd.Flags().GetInt("limit"); n > 0 {
			path += "&limit=" + strconv.Itoa(n)
		}

		resp, err := getClient().Get(path)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		runs, _ := result["runs"].([]interface{})

		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}

		headers := []string{"ID", "WORKFLOW", "STARTED"}
		var rows [][]string
		for _, r := range runs {
			rm, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := rm["id"].(string)
			wf, _ := rm["workflow"].(string)
			ts := formatRunTimestamp(rm["timestamp"])
			rows = append(rows, []string{id, wf, ts})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var runsOutputCmd = &cobra.Command{
	Use:   "output <runId>",
	Short: "Print the api_response output of a finished run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/slim/runs/" + args[0] + "/output"
		resp, err := getClient().Get(path)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			fmt.Print(string(resp.Body))
			return nil
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		return output.PrintJSON(result)
	},
}

// formatRunTimestamp accepts a value coming back as either int64 (Unix
// seconds) or float64 (JSON number) and returns a local-time string.
func formatRunTimestamp(v interface{}) string {
	var sec int64
	switch t := v.(type) {
	case float64:
		sec = int64(t)
	case int64:
		sec = t
	case int:
		sec = int64(t)
	default:
		return ""
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

func init() {
	runsListCmd.Flags().StringP("workflow", "w", "", "Filter by workflow name")
	runsListCmd.Flags().IntP("limit", "n", 50, "Max number of runs to show (server caps at 500)")

	runsCmd.AddCommand(runsListCmd)
	runsCmd.AddCommand(runsOutputCmd)

	// Nest runs under `dibbla wf runs ...` for consistency with `dibbla wf logs`.
	workflowsCmd.AddCommand(runsCmd)
}
