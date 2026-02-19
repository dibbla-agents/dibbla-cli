package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	deployForce   bool
	deployEnv     []string
	deployCPU     string
	deployMemory  string
	deployPort    string
)

func init() {
	rootCmd.AddCommand(deployCmd)
	deployCmd.Flags().BoolVarP(&deployForce, "force", "f", false, "Force redeploy if alias already exists")
	deployCmd.Flags().StringArrayVarP(&deployEnv, "env", "e", nil, "Set env var KEY=value (repeatable)")
	deployCmd.Flags().StringVar(&deployCPU, "cpu", "", "CPU request (e.g. 500m)")
	deployCmd.Flags().StringVar(&deployMemory, "memory", "", "Memory request (e.g. 512Mi)")
	deployCmd.Flags().StringVar(&deployPort, "port", "", "Container port (e.g. 3000)")
}

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an application to dibbla.app",
	Long: `Deploy a containerized application to the dibbla.app platform.

Your application will be available at https://<alias>.dibbla.app

Configuration:
  Set DIBBLA_API_TOKEN in your environment or .env file.
  Optionally set DIBBLA_API_URL to use a different API endpoint.

Examples:
  dibbla deploy              # Deploy current directory
  dibbla deploy ./myapp      # Deploy specific directory
  dibbla deploy --force      # Force redeploy existing alias
  dibbla deploy --cpu 500m --memory 512Mi --port 3000
  dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info`,
	Args: cobra.MaximumNArgs(1),
	Run:  runDeploy,
}

func runDeploy(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Dibbla Deploy\n", platform.Icon("🚀", ">>"))
	fmt.Println()

	// Load configuration
	cfg := config.Load()

	// Check for API token
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

	// Get path
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Printf("%s Error: Invalid path: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	// Check if path exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		fmt.Printf("%s Error: Directory not found: %s\n", platform.Icon("❌", "[X]"), absPath)
		os.Exit(1)
	}

	fmt.Printf("%s Deploying: %s\n", platform.Icon("📁", "[DIR]"), absPath)
	fmt.Printf("%s API: %s\n", platform.Icon("🌐", "[NET]"), cfg.APIURL)
	if deployForce {
		fmt.Printf("%s Force mode: will overwrite existing deployment\n", platform.Icon("⚠️", "[!]"))
	}
	fmt.Println()

	// Create and upload
	fmt.Printf("%s Creating archive...\n", platform.Icon("📦", "[PKG]"))

	opts := deploy.Options{
		APIURL:   cfg.APIURL,
		APIToken: cfg.APIToken,
		Path:     path,
		Force:    deployForce,
		Env:      deployEnv,
		CPU:      deployCPU,
		Memory:   deployMemory,
		Port:     deployPort,
	}

	fmt.Printf("%s Uploading and deploying...\n", platform.Icon("☁️", "[CLOUD]"))
	fmt.Println()

	// Show spinner while deploying
	done := make(chan struct{})
	go func() {
		if platform.SupportsUnicode() {
			spinStates := []string{
				"\033[32m⠋\033[0m", "\033[32m⠙\033[0m", "\033[32m⠹\033[0m", "\033[32m⠸\033[0m",
				"\033[32m⠼\033[0m", "\033[32m⠴\033[0m", "\033[32m⠦\033[0m", "\033[32m⠧\033[0m",
				"\033[32m⠇\033[0m", "\033[32m⠏\033[0m",
			}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r%s Deploying...", spinStates[i%len(spinStates)])
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
					fmt.Printf("\r[%s] Deploying...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
	}()

	result, err := deploy.Run(opts)
	close(done)
	if err != nil {
		fmt.Printf("\r%s Deployment failed: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	// Success output
	fmt.Printf("\r%s Deployment successful!\n", platform.Icon("✅", "[OK]"))
	fmt.Println()
	fmt.Printf("   URL:    %s\n", result.Deployment.URL)
	fmt.Printf("   Alias:  %s\n", result.Deployment.Alias)
	fmt.Printf("   Status: %s\n", result.Deployment.Status)
	fmt.Printf("   ID:     %s\n", result.Deployment.ID)

	if result.Deployment.HealthCheck != nil {
		fmt.Printf("   Health: %s (%dms)\n",
			result.Deployment.HealthCheck.Status,
			result.Deployment.HealthCheck.ResponseTimeMs)
	}
}
