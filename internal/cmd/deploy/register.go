package deploy

import (
	"fmt"
	"io"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

// Register adds all deploy-related commands to the root command.
func Register(root *cobra.Command) {
	root.AddCommand(appsCmd)
	root.AddCommand(deployCmd)
	root.AddCommand(dbCmd)
	root.AddCommand(storageCmd)
	root.AddCommand(secretsCmd)
}

func requireToken(cfg *config.Config) {
	if !cfg.HasToken() {
		fmt.Printf("%s Error: API token is required\n", platform.Icon("❌", "[X]"))
		fmt.Println()
		fmt.Println("Set your API token in one of these ways:")
		fmt.Println("  1. Run: dibbla login")
		fmt.Println("  2. Set DIBBLA_API_TOKEN in your environment or .env file")
		fmt.Println()
		fmt.Println("Get your API token at: https://app.dibbla.com/api-keys")
		os.Exit(1)
	}
}

// askConfirm reports the user's answer and, separately, whether they could
// be asked at all. The two are not the same outcome: see prompt.ErrNotInteractive.
func askConfirm(msg string) (bool, error) {
	return prompt.AskConfirmErr(msg)
}

// refuseUnconfirmable is what every confirm caller does when the prompt
// could not be shown. It is one function so all of them refuse identically.
//
// The outcome it replaces was the dangerous one: survey returns io.EOF
// without a terminal, the old code read that as a plain "no", printed
// "Cancelled." and exited 0. A script or coding agent driving the CLI was
// therefore told the command had succeeded in doing nothing, when in truth
// nobody had been asked. Exit 5 matches the CLI's "request validation /
// local refusal, zero requests made" code.
func refuseUnconfirmable(w io.Writer, action string) int {
	fmt.Fprintf(w, "%s %s needs confirmation, but stdin is not a terminal.\n",
		platform.Icon("❌", "[X]"), action)
	fmt.Fprintln(w, "  Re-run with --yes to confirm non-interactively, or run it from a terminal.")
	return 5
}
