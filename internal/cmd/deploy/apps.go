package deploy

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Manage Dibbla applications",
	Long:  `Provides commands to list and manage your Dibbla applications.`,
}

var appsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all deployed Dibbla applications",
	Long:  `Fetches and displays a list of all applications deployed to the Dibbla platform.`,
	Run:   runAppsList,
}

var appsDeleteCmd = &cobra.Command{
	Use:   "delete <alias>",
	Short: "Delete a Dibbla application",
	Long:  `Deletes a specific Dibbla application from the platform using its alias.`,
	Args:  cobra.ExactArgs(1),
	Run:   runAppsDelete,
}

var appsUpdateCmd = &cobra.Command{
	Use:   "update <alias>",
	Short: "Update a deployment",
	Long:  `Updates an existing deployment (env vars, replicas, cpu, memory, port).`,
	Args:  cobra.ExactArgs(1),
	Run:   runAppsUpdate,
}

var (
	deleteYes      bool
	updateEnv      []string
	updateReplicas int
	updateCPU      string
	updateMemory   string
	updatePort     int
)

func init() {
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsDeleteCmd)
	appsCmd.AddCommand(appsUpdateCmd)
	appsDeleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "Skip confirmation prompt")
	appsUpdateCmd.Flags().StringArrayVarP(&updateEnv, "env", "e", nil, "Set env var KEY=value (repeatable)")
	appsUpdateCmd.Flags().IntVar(&updateReplicas, "replicas", -1, "Desired number of replicas")
	appsUpdateCmd.Flags().StringVar(&updateCPU, "cpu", "", "CPU request/limit (e.g. 500m, 1)")
	appsUpdateCmd.Flags().StringVar(&updateMemory, "memory", "", "Memory request/limit (e.g. 256Mi, 512Mi)")
	appsUpdateCmd.Flags().IntVar(&updatePort, "port", -1, "Container port (1-65535)")
}

func runAppsList(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Retrieving Dibbla applications...\n", platform.Icon("🌱", "[>]"))
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

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
	requireToken(cfg)

	if !deleteYes {
		if !askConfirm(fmt.Sprintf("Are you sure you want to delete '%s'? This action cannot be undone.", alias)) {
			fmt.Println("Deletion cancelled.")
			os.Exit(0)
		}
	}

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
	requireToken(cfg)

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
