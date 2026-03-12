package wf

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var workflowsCmd = &cobra.Command{
	Use:     "workflows",
	Aliases: []string{"wf"},
	Short:   "Manage workflows",
}

var workflowsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workflows",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := getClient().Get("/api/wf/workflows?format=json")
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		workflows, _ := result["workflows"].([]interface{})
		if flagOutput == "json" {
			return output.PrintJSON(result)
		}
		if flagOutput == "yaml" {
			return output.PrintYAML(result)
		}
		headers := []string{"NAME", "LABEL", "NODES", "HAS_API"}
		var rows [][]string
		for _, w := range workflows {
			wf := w.(map[string]interface{})
			name, _ := wf["name"].(string)
			label, _ := wf["label"].(string)
			nodeCount := fmt.Sprintf("%v", wf["node_count"])
			hasAPI := fmt.Sprintf("%v", wf["has_api"])
			rows = append(rows, []string{name, label, nodeCount, hasAPI})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

var workflowsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a workflow definition",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/workflows/" + args[0] + "?format=json"
		revision, _ := cmd.Flags().GetString("revision")
		if revision != "" {
			path += "&revision=" + revision
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
		return output.PrintYAML(result)
	},
}

var workflowsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a workflow from file",
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		if filePath == "" {
			return fmt.Errorf("--file (-f) is required")
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		var body interface{}
		if err := parseFileContent(data, &body); err != nil {
			return err
		}
		resp, err := getClient().Post("/api/wf/workflows?include_result=true&format=json", body)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Created workflow %v (revision: %v)", result["name"], result["revision"])
		return printResult(result, "workflow")
	},
}

var workflowsUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Replace a workflow definition",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		if filePath == "" {
			return fmt.Errorf("--file (-f) is required")
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		var body interface{}
		if err := parseFileContent(data, &body); err != nil {
			return err
		}
		resp, err := getClient().Put("/api/wf/workflows/"+args[0]+"?include_result=true&format=json", body)
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Updated workflow %v", result["name"])
		return printResult(result, "workflow")
	},
}

var workflowsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !confirmAction(fmt.Sprintf("Delete workflow %q?", args[0]), yes) {
			return nil
		}
		resp, err := getClient().Delete("/api/wf/workflows/" + args[0] + "?format=json")
		if err != nil {
			return err
		}
		var result map[string]interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			return err
		}
		output.Stderr("Deleted workflow %v (revisions deleted: %v)", result["name"], result["revisions_deleted"])
		return nil
	},
}

var workflowsValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a workflow definition without saving",
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		if filePath == "" {
			return fmt.Errorf("--file (-f) is required")
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		var body interface{}
		if err := parseFileContent(data, &body); err != nil {
			return err
		}
		resp, err := getClient().Post("/api/wf/workflows/validate?format=json", body)
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

var workflowsExecuteCmd = &cobra.Command{
	Use:   "execute <name>",
	Short: "Execute a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/workflows/" + args[0] + "/execute?format=json"
		node, _ := cmd.Flags().GetString("node")
		if node != "" {
			path += "&node=" + node
		}
		dataStr, _ := cmd.Flags().GetString("data")
		filePath, _ := cmd.Flags().GetString("file")

		var body interface{}
		if filePath != "" {
			fileData, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			if err := json.Unmarshal(fileData, &body); err != nil {
				return fmt.Errorf("parsing JSON file: %w", err)
			}
		} else if dataStr != "" {
			if err := json.Unmarshal([]byte(dataStr), &body); err != nil {
				return fmt.Errorf("parsing --data JSON: %w", err)
			}
		} else {
			body = map[string]interface{}{}
		}
		resp, err := getClient().Post(path, body)
		if err != nil {
			return err
		}
		var result interface{}
		if err := parseJSON(resp.Body, &result); err != nil {
			fmt.Print(string(resp.Body))
			return nil
		}
		return output.PrintJSON(result)
	},
}

var workflowsURLCmd = &cobra.Command{
	Use:   "url <name>",
	Short: "Get the UI URL for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/workflows/" + args[0] + "/url?format=json"
		revision, _ := cmd.Flags().GetString("revision")
		if revision != "" {
			path += "&revision=" + revision
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
		fmt.Println(result["url"])
		return nil
	},
}

var workflowsAPIDocsCmd = &cobra.Command{
	Use:   "api-docs <name>",
	Short: "Show API endpoint documentation for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/wf/workflows/" + args[0] + "/api-docs?format=json"
		revision, _ := cmd.Flags().GetString("revision")
		if revision != "" {
			path += "&revision=" + revision
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
		endpoints, _ := result["endpoints"].([]interface{})
		if len(endpoints) == 0 {
			fmt.Println("No API endpoints found for this workflow.")
			return nil
		}
		for i, ep := range endpoints {
			e, ok := ep.(map[string]interface{})
			if !ok {
				continue
			}
			if i > 0 {
				fmt.Println("---")
			}
			executeURL, _ := e["execute_url"].(string)
			fmt.Printf("Endpoint: POST %s\n", executeURL)

			if urlIDs, ok := e["url_ids"].([]interface{}); ok && len(urlIDs) > 0 {
				var ids []string
				for _, id := range urlIDs {
					ids = append(ids, fmt.Sprintf("%v", id))
				}
				fmt.Printf("URL IDs:  %s\n", joinStrings(ids, ", "))
			}

			fmt.Println()

			if inputSchema, ok := e["input_schema"].(map[string]interface{}); ok && len(inputSchema) > 0 {
				fmt.Println("Request body:")
				for k, v := range inputSchema {
					fmt.Printf("  %-30s %v\n", k, v)
				}
				fmt.Println()
			}

			if outputSchema, ok := e["output_schema"].(map[string]interface{}); ok && len(outputSchema) > 0 {
				fmt.Println("Response:")
				for k, v := range outputSchema {
					fmt.Printf("  %-30s %v\n", k, v)
				}
				fmt.Println()
			}

			if executeURL != "" {
				fmt.Println("Example:")
				if inputSchema, ok := e["input_schema"].(map[string]interface{}); ok && len(inputSchema) > 0 {
					dataMap := make(map[string]interface{})
					for k, v := range inputSchema {
						dataMap[k] = exampleValue(fmt.Sprintf("%v", v))
					}
					dataJSON, _ := json.Marshal(dataMap)
					fmt.Printf("  curl -X POST %q \\\n", executeURL)
					fmt.Printf("    -H \"Content-Type: application/json\" \\\n")
					fmt.Printf("    -d '%s'\n", string(dataJSON))
				} else {
					fmt.Printf("  curl -X POST %q\n", executeURL)
				}
			}
			fmt.Println()
		}
		return nil
	},
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

func exampleValue(typeName string) interface{} {
	switch typeName {
	case "number", "float", "float64":
		return 42
	case "bool", "boolean":
		return true
	case "int", "integer":
		return 42
	default:
		return "example_value"
	}
}

func parseFileContent(data []byte, out interface{}) error {
	if err := json.Unmarshal(data, out); err == nil {
		return nil
	}
	return yaml.Unmarshal(data, out)
}

// printJSON and printYAML are package-local aliases used by printResult.
var printJSON = output.PrintJSON
var printYAML = output.PrintYAML

func init() {
	workflowsGetCmd.Flags().String("revision", "", "Revision ID")
	workflowsCreateCmd.Flags().StringP("file", "f", "", "Workflow definition file (YAML/JSON)")
	workflowsUpdateCmd.Flags().StringP("file", "f", "", "Workflow definition file (YAML/JSON)")
	workflowsDeleteCmd.Flags().Bool("yes", false, "Skip confirmation")
	workflowsValidateCmd.Flags().StringP("file", "f", "", "Workflow definition file (YAML/JSON)")
	workflowsExecuteCmd.Flags().String("data", "", "JSON data to send")
	workflowsExecuteCmd.Flags().StringP("file", "f", "", "JSON data file")
	workflowsExecuteCmd.Flags().String("node", "", "Target API node ID")
	workflowsURLCmd.Flags().String("revision", "", "Revision ID")
	workflowsAPIDocsCmd.Flags().String("revision", "", "Revision ID")

	workflowsCmd.AddCommand(workflowsListCmd)
	workflowsCmd.AddCommand(workflowsGetCmd)
	workflowsCmd.AddCommand(workflowsCreateCmd)
	workflowsCmd.AddCommand(workflowsUpdateCmd)
	workflowsCmd.AddCommand(workflowsDeleteCmd)
	workflowsCmd.AddCommand(workflowsValidateCmd)
	workflowsCmd.AddCommand(workflowsExecuteCmd)
	workflowsCmd.AddCommand(workflowsURLCmd)
	workflowsCmd.AddCommand(workflowsAPIDocsCmd)
}
