package deploy

import (
	"fmt"
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
	root.AddCommand(secretsCmd)
}

func requireToken(cfg *config.Config) {
	if !cfg.HasToken() {
		fmt.Printf("%s Error: DIBBLA_API_TOKEN is required\n", platform.Icon("❌", "[X]"))
		fmt.Println()
		fmt.Println("Set your API token in one of these ways:")
		fmt.Println("  1. Create a .env file with: DIBBLA_API_TOKEN=your_token")
		fmt.Println("  2. Export environment variable: export DIBBLA_API_TOKEN=your_token")
		fmt.Println()
		fmt.Println("Get your API token at: https://app.dibbla.com/settings/api-tokens")
		os.Exit(1)
	}
}

func askConfirm(msg string) bool {
	return prompt.AskConfirm(msg)
}
