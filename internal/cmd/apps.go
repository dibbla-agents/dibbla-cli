package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt" // Import for confirmation prompt
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(appsCmd)
	appsCmd.AddCommand(listCmd)
	appsCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Skip confirmation prompt")
}

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Manage Dibbla applications",
	Long:  `Provides commands to list and manage your Dibbla applications.`,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all deployed Dibbla applications",
	Long:  `Fetches and displays a list of all applications deployed to the Dibbla platform.`,
	Run:   runAppsList,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <alias>",
	Short: "Delete a Dibbla application",
	Long:  `Deletes a specific Dibbla application from the platform using its alias.`,
	Args:  cobra.ExactArgs(1), // Requires exactly one argument: the alias
	Run:   runAppsDelete,
}

var deleteYes bool

func runAppsList(cmd *cobra.Command, args []string) {
	fmt.Println("🌱 Retrieving Dibbla applications...")
	fmt.Println()

	cfg := config.Load()

	if !cfg.HasToken() {
		fmt.Println("❌ Error: DIBBLA_API_TOKEN is required")
		fmt.Println()
		fmt.Println("Set your API token in one of these ways:")
		fmt.Println("  1. Create a .env file with: DIBBLA_API_TOKEN=your_token")
		fmt.Println("  2. Export environment variable: export DIBBLA_API_TOKEN=your_token")
		fmt.Println()
		fmt.Println("Get your API token at: https://app.dibbla.com/settings/api-tokens")
		os.Exit(1)
	}

	deployments, err := apps.ListApps(cfg.APIURL, cfg.APIToken)
	if err != nil {
		fmt.Printf("❌ Failed to list applications: %v\n", err)
		os.Exit(1)
	}

	if len(deployments.Deployments) == 0 {
		fmt.Println("No applications deployed yet.")
		return
	}

	fmt.Printf("Found %d applications:\n", deployments.Total)
	fmt.Println()

	// Print header
	fmt.Printf("%-20s %-40s %-15s %s\n", "ALIAS", "URL", "STATUS", "LAST DEPLOYED")
	fmt.Printf("%-20s %-40s %-15s %s\n", "-----", "---", "------", "-------------")

	for _, dep := range deployments.Deployments {
		deployedAt := "N/A"
		if dep.DeployedAt != nil {
			deployedAt = dep.DeployedAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-20s %-40s %-15s %s\n", dep.Alias, dep.URL, dep.Status, deployedAt)
	}
}

func runAppsDelete(cmd *cobra.Command, args []string) {
	alias := args[0]
	fmt.Printf("🗑️  Attempting to delete application '%s'...\n", alias)
	fmt.Println()

	cfg := config.Load()

	if !cfg.HasToken() {
		fmt.Println("❌ Error: DIBBLA_API_TOKEN is required")
		fmt.Println()
		fmt.Println("Set your API token in one of these ways:")
		fmt.Println("  1. Create a .env file with: DIBBLA_API_TOKEN=your_token")
		fmt.Println("  2. Export environment variable: export DIBBLA_API_TOKEN=your_token")
		fmt.Println()
		fmt.Println("Get your API token at: https://app.dibbla.com/settings/api-tokens")
		os.Exit(1)
	}

	if !deleteYes {
		if !prompt.AskConfirm(fmt.Sprintf("Are you sure you want to delete '%s'? This action cannot be undone.", alias)) {
			fmt.Println("Deletion cancelled.")
			os.Exit(0)
		}
	}

	// Show red brail spinner while deleting
	done := make(chan struct{})
	go func() {
		// Red brail spinner sequence
		spinStates := []string{
			"\033[31m⠋\033[0m", "\033[31m⠙\033[0m", "\033[31m⠹\033[0m", "\033[31m⠸\033[0m",
			"\033[31m⠼\033[0m", "\033[31m⠴\033[0m", "\033[31m⠦\033[0m", "\033[31m⠧\033[0m",
			"\033[31m⠇\033[0m", "\033[31m⠏\033[0m",
		}
		i := 0
		for {
			select {
			case <-done:
				fmt.Printf("\r \r") // clear
				return
			default:
				fmt.Printf("\r%s Deleting...", spinStates[i%len(spinStates)])
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()

	deleteResponse, err := apps.DeleteApp(cfg.APIURL, cfg.APIToken, alias)
	if err != nil {
		fmt.Printf("❌ Failed to delete application '%s': %v\n", alias, err)
		os.Exit(1)
	}

	fmt.Printf("✅ %s\n", deleteResponse.Message)
}
