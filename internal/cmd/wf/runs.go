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

		headers := []string{"ID", "WORKFLOW", "STARTED", "ORIGIN"}
		var rows [][]string
		for _, r := range runs {
			rm, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := rm["id"].(string)
			wf, _ := rm["workflow"].(string)
			ts := formatRunTimestamp(rm["timestamp"])
			rows = append(rows, []string{id, wf, ts, formatRunOrigin(rm)})
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

// runsPurgeCmd is the ONLY destructive operation on run data (DIB-453):
// admin-only (the server enforces the role — this command merely surfaces
// it), explicitly invoked, never scheduled.
var runsPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Permanently delete run history (admin only)",
	Long: `Permanently delete run history. This is the only destructive operation on
run data and requires the admin or owner role (enforced server-side).

  --workflow <name> --before <date>   delete runs and events older than the
                                      date (YYYY-MM-DD, RFC3339 or unix
                                      seconds); running runs are never touched
  --workflow <name> --all             fully erase a DELETED workflow: its
                                      revisions, run history and tombstone.
                                      Refused for live workflows — delete
                                      first, then purge.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		workflow, _ := cmd.Flags().GetString("workflow")
		before, _ := cmd.Flags().GetString("before")
		all, _ := cmd.Flags().GetBool("all")
		if workflow == "" {
			return fmt.Errorf("--workflow is required")
		}
		if all == (before != "") {
			return fmt.Errorf("pass exactly one of --before <date> or --all")
		}

		yes, _ := cmd.Flags().GetBool("yes")
		var prompt string
		if all {
			prompt = fmt.Sprintf("PERMANENTLY erase deleted workflow %q — revisions, run history and cost records? This cannot be undone.", workflow)
		} else {
			prompt = fmt.Sprintf("PERMANENTLY delete run history of %q older than %s? This cannot be undone.", workflow, before)
		}
		if !confirmAction(prompt, yes) {
			return nil
		}

		path := "/api/wf/slim/workflows/" + workflow + "/purge?format=json"
		if all {
			path += "&all=true"
		} else {
			path += "&before=" + before
		}
		resp, err := getClient().Post(path, nil)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Purged %v: runs deleted %v, events deleted %v, revisions deleted %v",
			result["workflow"], result["runs_deleted"], result["events_deleted"], result["revisions_deleted"])
		return nil
	},
}

// formatRunOrigin renders the run-origin stamp for the ORIGIN column, blank
// for runs with no stamp (the common case — hand-triggered and old-SDK runs).
// The server projects origin_kind/origin_id only when present, so their
// absence is exactly "no provenance". A stamped run shows "<kind>:<id>", e.g.
// "app:lumen" or "pipeline_task:pipeline-abc-123", so the tutorial's stage 4
// can point at "your app did this" rather than a bare run id.
func formatRunOrigin(rm map[string]interface{}) string {
	kind, _ := rm["origin_kind"].(string)
	if kind == "" {
		return ""
	}
	if id, _ := rm["origin_id"].(string); id != "" {
		return kind + ":" + id
	}
	return kind
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

	runsPurgeCmd.Flags().String("workflow", "", "Workflow name (or tombstone key) to purge")
	runsPurgeCmd.Flags().String("before", "", "Delete run data older than this date (YYYY-MM-DD, RFC3339 or unix seconds)")
	runsPurgeCmd.Flags().Bool("all", false, "Fully erase a deleted workflow (refused for live workflows)")
	runsPurgeCmd.Flags().Bool("yes", false, "Skip confirmation")

	runsCmd.AddCommand(runsListCmd)
	runsCmd.AddCommand(runsOutputCmd)
	runsCmd.AddCommand(runsPurgeCmd)

	// Nest runs under `dibbla wf runs ...` for consistency with `dibbla wf logs`.
	workflowsCmd.AddCommand(runsCmd)
}
