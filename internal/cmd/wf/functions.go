package wf

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
)

var functionsCmd = &cobra.Command{
	Use:     "functions",
	Aliases: []string{"fn"},
	Short:   "Browse function registry",
}

var functionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available functions",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/slim/functions?format=json"
		server, _ := cmd.Flags().GetString("server")
		tag, _ := cmd.Flags().GetString("tag")
		if server != "" {
			path += "&server=" + server
		}
		if tag != "" {
			path += "&tag=" + tag
		}
		resp, err := getClient().Get(path)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		functions, _ := result["functions"].([]interface{})
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		headers := []string{"NAME", "SERVER", "DESCRIPTION", "TOOLS"}
		var rows [][]string
		for _, f := range functions {
			fn := f.(map[string]interface{})
			name, _ := fn["name"].(string)
			srv, _ := fn["server"].(string)
			desc, _ := fn["description"].(string)
			acceptsTools := ""
			if at, ok := fn["accepts_tools"].(bool); ok && at {
				acceptsTools = "yes"
			}
			rows = append(rows, []string{name, srv, desc, acceptsTools})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var functionsGetCmd = &cobra.Command{
	Use:   "get <server> <name>",
	Short: "Get function details",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/slim/functions/" + args[0] + "/" + args[1] + "?format=json")
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
		return output.PrintYAML(result)
	},
}

// functionsProvidersCmd lists capability providers — worker-registered
// replacements for a platform built-in (seats: tool_search, memory). They
// live in their own registry and never appear in `fn list`.
var functionsProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List registered capability providers",
	Long: `List the capability providers registered by connected workers.

A capability provider replaces the platform's built-in implementation behind
one agent capability seat (tool_search, memory). Workflows opt in per agent
node via 'capability_providers: {<seat>: "<name>"}'. Most workflows use the
built-ins and need no provider.

PORTS lists any extra input/output ports the provider declares. Once its seat
is bound, those are wireable on the agent node as '<seat>.<port>' — e.g.
'edges: [classify.topic -> agent.memory.topic]'. A starred input is required
and gates the node like any other required input. Use -o json for the full
JSON Schemas (types, descriptions).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/capability-providers"
		if capability, _ := cmd.Flags().GetString("capability"); capability != "" {
			path += "?capability=" + capability
		}
		resp, err := getClient().Get(path)
		if err != nil {
			var apiErr *apiclient.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				return fmt.Errorf("this workflow server does not support capability providers (endpoint not found) — it predates the provider registry; upgrade the workflow-server deployment to use this command")
			}
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		providers, _ := result["providers"].([]interface{})
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		headers := []string{"NAME", "SEAT", "SERVER", "VERSION", "PORTS", "DESCRIPTION"}
		var rows [][]string
		for _, pr := range providers {
			p, ok := pr.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := p["name"].(string)
			seat, _ := p["capability"].(string)
			srv, _ := p["server"].(string)
			version, _ := p["version"].(string)
			desc, _ := p["description"].(string)
			ports := summarizeExtraPorts(seat, p["extra_inputs_schema"], p["extra_outputs_schema"])
			rows = append(rows, []string{name, seat, srv, version, ports, desc})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

// summarizeExtraPorts renders a provider's declared extra ports for the table:
// the wireable "<seat>.<port>" names a workflow can address, with required
// inputs starred. Providers that declare none (the common case) get an empty
// cell. Full JSON Schemas stay available via -o json.
func summarizeExtraPorts(seat string, inSchema, outSchema interface{}) string {
	var parts []string
	if names := extraPortNames(seat, inSchema, true); names != "" {
		parts = append(parts, "in "+names)
	}
	if names := extraPortNames(seat, outSchema, false); names != "" {
		parts = append(parts, "out "+names)
	}
	return strings.Join(parts, " · ")
}

// extraPortNames pulls the property names out of one extra-port JSON Schema,
// sorted for a stable table. Anything that is not a schema object with
// properties yields "" — a provider is free to declare nothing.
func extraPortNames(seat string, schema interface{}, markRequired bool) string {
	obj, ok := schema.(map[string]interface{})
	if !ok {
		return ""
	}
	props, ok := obj["properties"].(map[string]interface{})
	if !ok || len(props) == 0 {
		return ""
	}
	required := map[string]bool{}
	if markRequired {
		if list, ok := obj["required"].([]interface{}); ok {
			for _, r := range list {
				if s, ok := r.(string); ok {
					required[s] = true
				}
			}
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		label := seat + "." + name
		if required[name] {
			label += "*"
		}
		names = append(names, label)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func init() {
	functionsListCmd.Flags().String("server", "", "Filter by server name")
	functionsListCmd.Flags().String("tag", "", "Filter by tag")
	functionsCmd.AddCommand(functionsListCmd)
	functionsCmd.AddCommand(functionsGetCmd)
	functionsProvidersCmd.Flags().String("capability", "", "Filter by capability seat (tool_search, memory)")
	functionsCmd.AddCommand(functionsProvidersCmd)
}
