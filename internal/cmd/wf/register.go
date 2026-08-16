package wf

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

// graphCommands are the workflow-editing command groups that live at the top
// level rather than under `wf`. They operate on the same workflows, so reaching
// for `dibbla wf tools add` is the natural guess — and cobra answers an unknown
// subcommand by printing the parent's help and exiting 0, which reads as
// success. rejectUnknownSubcommand turns that into a redirect.
var graphCommands = map[string]string{
	"nodes":     "dibbla nodes",
	"edges":     "dibbla edges",
	"inputs":    "dibbla inputs",
	"tools":     "dibbla tools",
	"revisions": "dibbla revisions",
	"functions": "dibbla functions",
}

// rejectUnknownSubcommand fails instead of silently printing help, and points
// at the real command when the caller guessed a top-level group.
func rejectUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if target, ok := graphCommands[args[0]]; ok {
		return fmt.Errorf("%q is not a subcommand of %q — use `%s %s` instead",
			args[0], cmd.Name(), target, strings.Join(args[1:], " "))
	}
	return fmt.Errorf("unknown subcommand %q for %q — run `dibbla %s --help` to see the available ones",
		args[0], cmd.Name(), cmd.Name())
}

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
		cmd.RunE = rejectUnknownSubcommand
		cmd.Args = cobra.ArbitraryArgs
		cmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "Output format: yaml/json/table")
		cmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Minimal output")
		cmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show HTTP request/response")
		// Render API errors, suppress the usage dump that otherwise buries
		// them, and attach a meaningful exit code.
		harden(cmd)
		root.AddCommand(cmd)
	}
}

func initClient(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	if !cfg.HasToken() {
		return fmt.Errorf("API token required: run dibbla login or set DIBBLA_API_TOKEN")
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
	printWarnings(result)
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
