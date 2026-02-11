package cmd

import (
	"fmt"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/apps" // Will create this package
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(appsCmd)
	appsCmd.AddCommand(listCmd)
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
