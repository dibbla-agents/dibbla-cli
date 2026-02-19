package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(appsCmd)
	appsCmd.AddCommand(listCmd)
	appsCmd.AddCommand(deleteCmd)
	appsCmd.AddCommand(updateCmd)
	deleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Skip confirmation prompt")
	updateCmd.Flags().StringArrayVarP(&updateEnv, "env", "e", nil, "Set env var KEY=value (repeatable)")
	updateCmd.Flags().IntVar(&updateReplicas, "replicas", -1, "Desired number of replicas")
	updateCmd.Flags().StringVar(&updateCPU, "cpu", "", "CPU request/limit (e.g. 500m, 1)")
	updateCmd.Flags().StringVar(&updateMemory, "memory", "", "Memory request/limit (e.g. 256Mi, 512Mi)")
	updateCmd.Flags().IntVar(&updatePort, "port", -1, "Container port (1-65535)")
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
	Args:  cobra.ExactArgs(1),
	Run:   runAppsDelete,
}

var updateCmd = &cobra.Command{
	Use:   "update <alias>",
	Short: "Update a deployment",
	Long:  `Updates an existing deployment (env vars, replicas, cpu, memory, port).`,
	Args:  cobra.ExactArgs(1),
	Run:   runAppsUpdate,
}

var deleteYes bool
var updateEnv      []string
var updateReplicas int
var updateCPU      string
var updateMemory   string
var updatePort     int

func runAppsList(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Retrieving Dibbla applications...\n", platform.Icon("🌱", "[>]"))
	fmt.Println()

	cfg := config.Load()

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

	deployments, err := apps.ListApps(cfg.APIURL, cfg.APIToken)
	if err != nil {
		fmt.Printf("%s Failed to list applications: %v\n", platform.Icon("❌", "[X]"), err)
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
	fmt.Printf("%s Attempting to delete application '%s'...\n", platform.Icon("🗑️", "[DEL]"), alias)
	fmt.Println()

	cfg := config.Load()

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

	if !deleteYes {
		if !prompt.AskConfirm(fmt.Sprintf("Are you sure you want to delete '%s'? This action cannot be undone.", alias)) {
			fmt.Println("Deletion cancelled.")
			os.Exit(0)
		}
	}

	// Show spinner while deleting
	done := make(chan struct{})
	go func() {
		if platform.SupportsUnicode() {
			spinStates := []string{
				"\033[31m⠋\033[0m", "\033[31m⠙\033[0m", "\033[31m⠹\033[0m", "\033[31m⠸\033[0m",
				"\033[31m⠼\033[0m", "\033[31m⠴\033[0m", "\033[31m⠦\033[0m", "\033[31m⠧\033[0m",
				"\033[31m⠇\033[0m", "\033[31m⠏\033[0m",
			}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r%s Deleting...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		} else {
			spinStates := []string{"|", "/", "-", "\\"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r[%s] Deleting...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
	}()

	deleteResponse, err := apps.DeleteApp(cfg.APIURL, cfg.APIToken, alias)
	close(done)
	if err != nil {
		fmt.Printf("\r%s Failed to delete application '%s': %v\n", platform.Icon("❌", "[X]"), alias, err)
		os.Exit(1)
	}

	fmt.Printf("\r%s %s\n", platform.Icon("✅", "[OK]"), deleteResponse.Message)
}

func runAppsUpdate(cmd *cobra.Command, args []string) {
	alias := args[0]
	cfg := config.Load()

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

	envMap := envPairsToMap(updateEnv)
	var replicas *int32
	if updateReplicas >= 0 {
		r := int32(updateReplicas)
		replicas = &r
	}
	var port *int
	if updatePort >= 1 && updatePort <= 65535 {
		port = &updatePort
	} else if updatePort >= 0 {
		fmt.Printf("%s Error: --port must be between 1 and 65535\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	hasUpdate := len(envMap) > 0 || replicas != nil || updateCPU != "" || updateMemory != "" || port != nil
	if !hasUpdate {
		fmt.Printf("%s Error: specify at least one of --env (-e), --replicas, --cpu, --memory, or --port\n", platform.Icon("❌", "[X]"))
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  dibbla apps update myapp -e NODE_ENV=production")
		fmt.Println("  dibbla apps update myapp --replicas 3")
		fmt.Println("  dibbla apps update myapp --cpu 500m --memory 512Mi --port 3000")
		os.Exit(1)
	}

	req := apps.UpdateDeploymentRequest{
		EnvironmentVariables: envMap,
		Replicas:             replicas,
		CPU:                  updateCPU,
		Memory:               updateMemory,
		Port:                 port,
	}

	fmt.Printf("%s Updating deployment '%s'...\n", platform.Icon("✏️", "[UPDATE]"), alias)
	fmt.Println()

	dep, err := apps.UpdateApp(cfg.APIURL, cfg.APIToken, alias, req)
	if err != nil {
		fmt.Printf("%s Update failed: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s Deployment updated successfully.\n", platform.Icon("✅", "[OK]"))
	fmt.Println()
	fmt.Printf("   Alias:  %s\n", dep.Alias)
	fmt.Printf("   URL:    %s\n", dep.URL)
	fmt.Printf("   Status: %s\n", dep.Status)
	if dep.HealthCheck != nil {
		fmt.Printf("   Health: %s (%dms)\n", dep.HealthCheck.Status, dep.HealthCheck.ResponseTimeMs)
	}
}

// envPairsToMap converts KEY=value pairs into a map (splits on first "=").
func envPairsToMap(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		idx := strings.Index(p, "=")
		if idx <= 0 {
			continue
		}
		m[p[:idx]] = p[idx+1:]
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
