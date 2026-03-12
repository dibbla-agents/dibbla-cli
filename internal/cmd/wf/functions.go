package wf

import (
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

func init() {
	functionsListCmd.Flags().String("server", "", "Filter by server name")
	functionsListCmd.Flags().String("tag", "", "Filter by tag")
	functionsCmd.AddCommand(functionsListCmd)
	functionsCmd.AddCommand(functionsGetCmd)
}
