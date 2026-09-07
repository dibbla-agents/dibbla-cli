package wf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/applogs"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

// Function exposure (DIB-764). A registered function is callable from a
// workflow by anyone who can edit workflows; exposing it additionally makes
// it a tool on the /platform/tools MCP connector, callable by every member
// whose organization role is at least the exposure's min_role. The policy is
// default-deny and org-scoped, capped at 100 enabled exposures, and every
// call through it is recorded as a tool invocation — a sibling of a workflow
// run, never a run — with its own logs.

// exposureRoles is the org role ladder the server accepts for --min-role.
// There is no "member" role: "any member" is spelled viewer.
var exposureRoles = []string{"viewer", "developer", "admin", "owner"}

const exposureLimit = 100

var functionsExposedCmd = &cobra.Command{
	Use:   "exposed",
	Short: "List the functions exposed as MCP tools",
	Long: `List this organization's function exposures — the functions members can call
as tools on the /platform/tools MCP connector.

A function is exposed with 'dibbla fn expose <server> <name>'. Members whose
organization role is at least the exposure's MIN ROLE see it as a tool;
ENABLED false keeps the row but hides the tool. REGISTERED false means the
function's worker is not connected right now: the exposure stays, the tool
is not offered until the function registers again.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/slim/tool-exposures?format=json")
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		exposures, _ := result["exposures"].([]interface{})
		headers := []string{"NAME", "SERVER", "MIN ROLE", "ENABLED", "REGISTERED"}
		var rows [][]string
		for _, e := range exposures {
			row, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, exposureRow(row))
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var functionsExposeCmd = &cobra.Command{
	Use:   "expose <server> <name>",
	Short: "Expose a function as an MCP tool (admin only)",
	Long: `Expose a registered function as a tool on the /platform/tools MCP connector.

Once exposed, every member of this organization whose role is at least
--min-role (viewer < developer < admin < owner; default viewer, i.e. any
member) can call the function as an MCP tool from their agent. Each call is
recorded as a tool invocation — see 'dibbla fn invocations'.

Requires the admin or owner role. The function must be registered right now
('dibbla fn list'); it may go unregistered later, which 'fn exposed' shows as
REGISTERED false. An organization can enable at most 100 exposures. Running
'expose' again on an exposed function updates its min role and enabled state.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, name := args[0], args[1]
		minRole, _ := cmd.Flags().GetString("min-role")
		disabled, _ := cmd.Flags().GetBool("disabled")
		if !validExposureRole(minRole) {
			return fmt.Errorf("--min-role must be one of %s, got %q", strings.Join(exposureRoles, ", "), minRole)
		}
		body := map[string]interface{}{
			"min_role": minRole,
			"enabled":  !disabled,
		}
		resp, err := getClient().Put(exposurePath(server, name), body)
		if err != nil {
			return renderExposeError(err, server, name)
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		return printResult(result, "exposure")
	},
}

var functionsUnexposeCmd = &cobra.Command{
	Use:   "unexpose <server> <name>",
	Short: "Stop exposing a function as an MCP tool (admin only)",
	Long: `Remove a function's exposure so it is no longer offered as an MCP tool.

Members lose the tool at their next tool listing; invocations already
recorded are kept. Requires the admin or owner role. To keep the exposure
but hide the tool temporarily, use 'dibbla fn expose <server> <name> --disabled'.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, name := args[0], args[1]
		yes, _ := cmd.Flags().GetBool("yes")
		if !confirmAction(fmt.Sprintf("Stop exposing %s/%s as an MCP tool?", server, name), yes) {
			return nil
		}
		if _, err := getClient().Delete(exposurePath(server, name)); err != nil {
			return renderExposeError(err, server, name)
		}
		output.Stderr("Function %s/%s is no longer exposed", server, name)
		return nil
	},
}

var functionsInvocationsCmd = &cobra.Command{
	Use:   "invocations",
	Short: "List calls made to exposed functions",
	Long: `List tool invocations — the calls members (or their agents) made to exposed
functions through the /platform/tools MCP connector or the API, newest first.

An invocation is not a workflow run: it does not appear in 'dibbla wf runs
list'. Use 'dibbla fn invocation <id>' for one call's full record and logs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		q := url.Values{}
		for _, f := range []string{"server", "function", "source", "user", "since"} {
			if v, _ := cmd.Flags().GetString(f); v != "" {
				q.Set(f, v)
			}
		}
		if n, _ := cmd.Flags().GetInt("limit"); n > 0 {
			q.Set("limit", strconv.Itoa(n))
		}
		path := "/api/wf/slim/tool-invocations?format=json"
		if enc := q.Encode(); enc != "" {
			path += "&" + enc
		}
		resp, err := getClient().Get(path)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		invocations, _ := result["invocations"].([]interface{})
		headers := []string{"ID", "WHEN", "FUNCTION", "SOURCE", "USER", "STATUS", "DURATION", "ERROR"}
		var rows [][]string
		for _, i := range invocations {
			row, ok := i.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, invocationRow(row))
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var functionsInvocationCmd = &cobra.Command{
	Use:   "invocation <id>",
	Short: "Show one call made to an exposed function",
	Long: `Show the record of one tool invocation: who called which exposed function,
from where, its status, duration, and the sizes and digests of its input and
output. With --logs, the log lines the function emitted while it ran follow
the record, in the same format as 'dibbla wf logs'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		resp, err := getClient().Get("/api/wf/slim/tool-invocations/" + id + "?format=json")
		if err != nil {
			var apiErr *apiclient.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				return failWithStatus(404, "invocation %q not found in your organization — `dibbla fn invocations` lists recent ones", id)
			}
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		if flagOutput == "json" {
			if err := output.PrintJSON(result); err != nil {
				return err
			}
		} else if err := output.PrintYAML(result); err != nil {
			return err
		}
		withLogs, _ := cmd.Flags().GetBool("logs")
		if !withLogs {
			return nil
		}
		// An invocation is bounded (the server times it out at 30 s) and
		// finished by the time anyone asks, so this is a backfill, not a
		// tail: everything from just before it started, at every level.
		opts := applogs.RunOptions{Level: "debug"}
		if started, ok := invocationStart(result); ok {
			opts.Since = started.Add(-time.Minute)
		}
		return tailLogStream(cmd, fmt.Sprintf("invocation %q", id),
			func(ctx context.Context, cfg *config.Config) (io.ReadCloser, error) {
				return applogs.StreamInvocation(ctx, cfg.APIURL, cfg.APIToken, id, opts)
			})
	},
}

func exposurePath(server, name string) string {
	return "/api/wf/slim/tool-exposures/" + url.PathEscape(server) + "/" + url.PathEscape(name) + "?format=json"
}

func validExposureRole(role string) bool {
	for _, r := range exposureRoles {
		if r == role {
			return true
		}
	}
	return false
}

// renderExposeError turns the exposure endpoints' refusals into the action
// that resolves them. The generic status guidance in errors.go is written
// for workflows ("`dibbla wf list` shows the workflows…") and would point
// the wrong way here.
func renderExposeError(err error, server, name string) error {
	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg, code := slimError(apiErr)
	switch {
	case apiErr.StatusCode == 409 && code == "EXPOSURE_LIMIT":
		return failWithStatus(409, "this organization already exposes %d functions; unexpose one first", exposureLimit)
	case apiErr.StatusCode == 400:
		return failWithStatus(400, "%s", msg)
	case apiErr.StatusCode == 403:
		return failWithStatus(403, "exposing functions requires the admin or owner role")
	case apiErr.StatusCode == 404:
		return failWithStatus(404, "function %s/%s is not registered — check `dibbla fn list`", server, name)
	}
	return err
}

// exposureRow renders one exposure for the table; the names match the
// server's row shape so -o json shows the same fields.
func exposureRow(row map[string]interface{}) []string {
	name, _ := row["function_name"].(string)
	server, _ := row["server"].(string)
	minRole, _ := row["min_role"].(string)
	return []string{name, server, minRole, formatBool(row["enabled"]), formatBool(row["registered"])}
}

// invocationRow renders one invocation for the table. ERROR prefers the
// human failure summary over the bare code; a completed call leaves it empty.
func invocationRow(row map[string]interface{}) []string {
	id, _ := row["id"].(string)
	server, _ := row["server"].(string)
	name, _ := row["function_name"].(string)
	source, _ := row["source"].(string)
	user, _ := row["triggered_by_user_id"].(string)
	status, _ := row["status"].(string)
	when := ""
	if started, ok := invocationStart(row); ok {
		when = started.Local().Format("2006-01-02 15:04:05")
	}
	errText, _ := row["failure_summary"].(string)
	if errText == "" {
		errText, _ = row["error_code"].(string)
	}
	return []string{id, when, server + "/" + name, source, user, status, formatDurationMS(row["duration_ms"]), errText}
}

// invocationStart reads started_at, which the server writes as RFC3339.
func invocationStart(row map[string]interface{}) (time.Time, bool) {
	raw, _ := row["started_at"].(string)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func formatBool(v interface{}) string {
	if b, ok := v.(bool); ok && b {
		return "true"
	}
	return "false"
}

// formatDurationMS renders a JSON number of milliseconds as "123ms" below a
// second and "1.2s" above; null (a call still running) is blank.
func formatDurationMS(v interface{}) string {
	ms, ok := v.(float64)
	if !ok {
		return ""
	}
	if ms < 1000 {
		return strconv.FormatInt(int64(ms), 10) + "ms"
	}
	return strconv.FormatFloat(ms/1000, 'f', 1, 64) + "s"
}

func init() {
	functionsExposeCmd.Flags().String("min-role", "viewer", "Lowest organization role that may call the tool: viewer, developer, admin or owner")
	functionsExposeCmd.Flags().Bool("disabled", false, "Record the exposure but do not offer the tool yet")

	functionsUnexposeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation")

	functionsInvocationsCmd.Flags().String("server", "", "Only calls to functions on this server")
	functionsInvocationsCmd.Flags().String("function", "", "Only calls to this function name")
	functionsInvocationsCmd.Flags().String("source", "", "Only calls from this source: mcp, api or cli")
	functionsInvocationsCmd.Flags().String("user", "", "Only calls triggered by this user id")
	functionsInvocationsCmd.Flags().String("since", "", "Only calls started after this time (RFC3339 or unix seconds)")
	functionsInvocationsCmd.Flags().IntP("limit", "n", 50, "Max number of invocations to show")

	functionsInvocationCmd.Flags().Bool("logs", false, "Also print the log lines the function emitted during the call")

	functionsCmd.AddCommand(functionsExposedCmd)
	functionsCmd.AddCommand(functionsExposeCmd)
	functionsCmd.AddCommand(functionsUnexposeCmd)
	functionsCmd.AddCommand(functionsInvocationsCmd)
	functionsCmd.AddCommand(functionsInvocationCmd)
}
