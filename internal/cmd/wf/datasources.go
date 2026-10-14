package wf

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

// Data sources outside workflows (DIB-1440, epic DIB-1432). An admin exposes
// a managed data source (Console → MCP, or the data-source-exposures API);
// members whose organization role meets the exposure's min_role can then ask
// it questions: semantic search, optionally narrowed by equality filters on
// the columns its filter_schema declares. It is the same slim endpoint the
// platform_data_sources MCP tool calls, so the answer is the same — only the
// audit row's source differs ("cli" here).

// dataSourceMaxTopK is the search engine's own cap, shared with workflow
// agents. The server silently cuts a larger top_k to it; the CLI refuses
// instead, so nobody reads 20 hits believing they asked for 50.
const dataSourceMaxTopK = 20

var dataSourcesCmd = &cobra.Command{
	Use:     "datasource",
	Aliases: []string{"datasources", "ds"},
	Short:   "Query the data sources your organization exposes to agents",
	Long: `Query the data sources your organization has exposed to agents — the same
sources, and the same answers, as the platform_data_sources tool on the Dibbla
MCP connector.

  dibbla datasource list
  dibbla datasource query voicelines "an angry line" --filter speaker=mario

A source is queryable when an admin has exposed it and your organization role
is at least its min role. Every query is recorded as a tool invocation with
source "cli"; it is not a workflow run and does not appear in
'dibbla fn invocations', which lists function calls only.`,
}

var dataSourcesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the data sources you can query",
	Long: `List the data sources you can query: exposed, switched on, and with a min
role your organization role meets. FILTERS are the columns a query can filter
on, with their types; DESCRIPTION says what the source holds.

With -o yaml or -o json the full entry is shown, including example questions
and the search targets (the configured tables, by label).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/slim/data-sources?format=json")
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
		sources, _ := result["data_sources"].([]interface{})
		if len(sources) == 0 {
			output.Stderr("No data sources you can query. An admin exposes one in Console → MCP; a source needs a min role your role meets.")
			return nil
		}
		headers := []string{"ID", "NAME", "MIN ROLE", "FILTERS", "DESCRIPTION"}
		var rows [][]string
		for _, s := range sources {
			row, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			rows = append(rows, dataSourceRow(row))
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var dataSourcesQueryCmd = &cobra.Command{
	Use:   "query <id> <question>",
	Short: "Ask a data source a question (semantic search)",
	Long: `Ask an exposed data source a question in natural language and print the
closest hits. <id> is the source's ID from 'dibbla datasource list'.

  dibbla datasource query voicelines "an angry line" --filter speaker=mario
  dibbla datasource query voicelines "a happy line" --filter speaker=mario --filter speaker=luigi --top-k 3

--filter column=value keeps hits whose column equals the value; repeat it for
the same column to allow any of several values, for different columns to
require all. Only the columns under FILTERS in the list can be filtered on.
--top-k is 10 by default and at most 20.

The answer is capped at 16 KiB: long values are shortened and, if that is not
enough, the last hits dropped. A cut answer says so (truncated: true and a
note) and the CLI repeats the note on stderr.

Refusals: an unknown, unexposed or switched-off source is one 404 (exit 4) on
purpose; a role below the source's min role is 403 (exit 3); a filter on an
undeclared column is 400 naming the filterable ones; too many queries is 429.
Every query past those checks is recorded as a tool invocation with source
"cli".`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, question := args[0], args[1]
		if strings.TrimSpace(question) == "" {
			return fmt.Errorf("the question is empty — pass it in quotes: dibbla datasource query %s \"an angry line\"", id)
		}
		body := map[string]interface{}{"query": question}

		rawFilters, _ := cmd.Flags().GetStringArray("filter")
		filters, err := parseDataSourceFilters(rawFilters)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			body["filters"] = filters
		}
		if cmd.Flags().Changed("top-k") {
			topK, _ := cmd.Flags().GetInt("top-k")
			if topK < 1 || topK > dataSourceMaxTopK {
				return failWithStatus(400, "--top-k must be between 1 and %d (the search engine's cap, shared with workflow agents); got %d", dataSourceMaxTopK, topK)
			}
			body["top_k"] = topK
		}
		if target, _ := cmd.Flags().GetString("target"); target != "" {
			body["target"] = target
		}
		if cmd.Flags().Changed("min-score") {
			minScore, _ := cmd.Flags().GetFloat64("min-score")
			body["min_score"] = minScore
		}

		path := "/api/wf/slim/data-sources/" + url.PathEscape(id) + "/query?format=json"
		resp, err := getClient().PostWithHeaders(path, body, map[string]string{"X-Dibbla-Source": "cli"})
		if err != nil {
			return renderDataSourceQueryError(err, id)
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		if truncated, _ := result["truncated"].(bool); truncated {
			note, _ := result["truncation_note"].(string)
			if note == "" {
				note = "the answer was cut to fit 16 KiB"
			}
			output.Stderr("Truncated: %s", note)
		}
		return printResult(result, "")
	},
}

// parseDataSourceFilters turns repeated --filter column=value flags into the
// query's filters: one value per column is that value, several are a list
// the column must be one of. Values go as strings; the source's filter_schema
// decides how the database compares them.
func parseDataSourceFilters(raw []string) (map[string]interface{}, error) {
	values := map[string][]string{}
	var order []string
	for _, f := range raw {
		col, val, ok := strings.Cut(f, "=")
		col = strings.TrimSpace(col)
		if !ok || col == "" {
			return nil, fmt.Errorf("--filter %q: expected column=value, e.g. --filter speaker=mario", f)
		}
		if _, seen := values[col]; !seen {
			order = append(order, col)
		}
		values[col] = append(values[col], val)
	}
	out := make(map[string]interface{}, len(values))
	for _, col := range order {
		vs := values[col]
		if len(vs) == 1 {
			out[col] = vs[0]
			continue
		}
		list := make([]interface{}, len(vs))
		for i, v := range vs {
			list[i] = v
		}
		out[col] = list
	}
	return out, nil
}

// renderDataSourceQueryError says what stopped the query in the caller's
// terms. The server answers one 404 for unknown, unexposed and switched-off
// sources on purpose, so the sentence names all of them.
func renderDataSourceQueryError(err error, id string) error {
	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg, code := slimError(apiErr)
	switch apiErr.StatusCode {
	case 404:
		return failWithStatus(404, "data source %q is not queryable for you (unknown, not exposed, or switched off) — `dibbla datasource list` lists the ones you can query", id)
	case 403:
		return failWithStatus(403, "%s — your organization role is below the source's min role; an admin can lower it in Console → MCP", msg)
	case 400:
		return failWithStatus(400, "query refused: %s", msg)
	case 429:
		return failWithStatus(429, "%s — try again later", msg)
	case 502:
		return failWithStatus(502, "query on %s failed: %s — the attempt is recorded as a tool invocation", id, msg)
	case 504:
		return failWithStatus(504, "%s — the attempt is recorded as timed out", msg)
	}
	if code != "" {
		return failWithStatus(apiErr.StatusCode, "%s (%s)", msg, code)
	}
	return err
}

// dataSourceRow renders one entry of GET /api/slim/data-sources. The ID
// column is source_id, the handle a query takes.
func dataSourceRow(row map[string]interface{}) []string {
	id, _ := row["source_id"].(string)
	if id == "" {
		id, _ = row["id"].(string)
	}
	name, _ := row["display_name"].(string)
	minRole, _ := row["min_role"].(string)
	desc, _ := row["description"].(string)
	return []string{id, name, minRole, formatFilterSchema(row["filters"]), desc}
}

// formatFilterSchema renders a filter_schema as "column (type), …", sorted,
// or "-" when the source has no filterable columns.
func formatFilterSchema(v interface{}) string {
	schema, _ := v.(map[string]interface{})
	if len(schema) == 0 {
		return "-"
	}
	cols := make([]string, 0, len(schema))
	for col := range schema {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	parts := make([]string, len(cols))
	for i, col := range cols {
		switch t := schema[col].(type) {
		case string:
			parts[i] = col + " (" + t + ")"
		case float64:
			parts[i] = col + " (" + strconv.FormatFloat(t, 'f', -1, 64) + ")"
		default:
			parts[i] = col
		}
	}
	return strings.Join(parts, ", ")
}

func init() {
	dataSourcesQueryCmd.Flags().StringArray("filter", nil, "Keep hits whose column equals the value: column=value (repeatable)")
	dataSourcesQueryCmd.Flags().Int("top-k", 10, "Number of hits, 1–20")
	dataSourcesQueryCmd.Flags().String("target", "", "Search only this search target (its label or table)")
	dataSourcesQueryCmd.Flags().Float64("min-score", 0, "Drop hits with a similarity below this (0–1)")

	dataSourcesCmd.AddCommand(dataSourcesListCmd)
	dataSourcesCmd.AddCommand(dataSourcesQueryCmd)
}
