package wf

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	flagOutput  string
	flagQuiet   bool
	flagVerbose bool
	apiClient   *apiclient.Client
)

// Register adds all workflow-related commands to the root command.
func Register(root *cobra.Command) {
	parents := []*cobra.Command{
		workflowsCmd,
		nodesCmd,
		edgesCmd,
		inputsCmd,
		toolsCmd,
		revisionsCmd,
		functionsCmd,
	}

	for _, cmd := range parents {
		cmd.PersistentPreRunE = initClient
		cmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "Output format: yaml/json/table")
		cmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Minimal output")
		cmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show HTTP request/response")
		root.AddCommand(cmd)
	}
}

func initClient(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	if !cfg.HasToken() {
		return fmt.Errorf("API token required: set DIBBLA_API_TOKEN or add it to .env")
	}
	apiClient = apiclient.NewClient(cfg.APIURL, cfg.APIToken, flagVerbose)
	return nil
}

func getClient() *apiclient.Client {
	return apiClient
}

func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// patchWorkflow sends a PATCH with operations and returns the updated workflow.
func patchWorkflow(name string, ops []map[string]interface{}) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"operations": ops,
	}
	resp, err := getClient().Patch("/api/wf/slim/workflows/"+name+"?include_result=true&format=json", body)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := parseJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// confirmAction prompts for confirmation. Returns true if confirmed or --yes is set.
func confirmAction(msg string, yes bool) bool {
	if yes {
		return true
	}
	fi, _ := os.Stdin.Stat()
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		fmt.Fprintf(os.Stderr, "Refusing destructive action in non-interactive mode. Use --yes to confirm.\n")
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", msg)
	var response string
	fmt.Scanln(&response)
	return response == "y" || response == "Y" || response == "yes"
}

// printResult outputs a workflow result according to the --output and --quiet flags.
func printResult(result map[string]interface{}, key string) error {
	if flagQuiet {
		return nil
	}
	data, ok := result[key]
	if !ok {
		data = result
	}
	if flagOutput == "json" {
		return printJSON(data)
	}
	return printYAML(data)
}
