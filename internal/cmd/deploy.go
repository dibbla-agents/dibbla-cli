package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/spf13/cobra"
)

var (
	deployForce bool
)

func init() {
	rootCmd.AddCommand(deployCmd)
	deployCmd.Flags().BoolVarP(&deployForce, "force", "f", false, "Force redeploy if alias already exists")
}

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an application to dibbla.app",
	Long: `Deploy a containerized application to the dibbla.app platform.

The target directory must contain a valid docker-compose.yml file.
Your application will be available at https://<alias>.dibbla.app

Configuration:
  Set DIBBLA_API_TOKEN in your environment or .env file.
  Optionally set DIBBLA_API_URL to use a different API endpoint.

Examples:
  dibbla deploy              # Deploy current directory
  dibbla deploy ./myapp      # Deploy specific directory
  dibbla deploy --force      # Force redeploy existing alias`,
	Args: cobra.MaximumNArgs(1),
	Run:  runDeploy,
}

func runDeploy(cmd *cobra.Command, args []string) {
	fmt.Println("🚀 Dibbla Deploy")
	fmt.Println()

	// Load configuration
	cfg := config.Load()

	// Check for API token
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

	// Get path
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Printf("❌ Error: Invalid path: %v\n", err)
		os.Exit(1)
	}

	// Check if path exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		fmt.Printf("❌ Error: Directory not found: %s\n", absPath)
		os.Exit(1)
	}

	// Check for docker-compose.yml
	composePath := filepath.Join(absPath, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Printf("❌ Error: docker-compose.yml not found in %s\n", absPath)
		os.Exit(1)
	}

	fmt.Printf("📁 Deploying: %s\n", absPath)
	fmt.Printf("🌐 API: %s\n", cfg.APIURL)
	if deployForce {
		fmt.Println("⚠️  Force mode: will overwrite existing deployment")
	}
	fmt.Println()

	// Create and upload
	fmt.Println("📦 Creating archive...")

	opts := deploy.Options{
		APIURL:   cfg.APIURL,
		APIToken: cfg.APIToken,
		Path:     path,
		Force:    deployForce,
	}

	fmt.Println("☁️  Uploading and deploying...")
	fmt.Println()

	// Show green brail spinner while deploying
	done := make(chan struct{})
	go func() {
		// Green brail spinner sequence
		spinStates := []string{
			"\033[32m⠋\033[0m", "\033[32m⠙\033[0m", "\033[32m⠹\033[0m", "\033[32m⠸\033[0m",
			"\033[32m⠼\033[0m", "\033[32m⠴\033[0m", "\033[32m⠦\033[0m", "\033[32m⠧\033[0m",
			"\033[32m⠇\033[0m", "\033[32m⠏\033[0m",
		}
		i := 0
		for {
			select {
			case <-done:
				fmt.Printf("\r \r") // clear
				return
			default:
				fmt.Printf("\r%s Deploying...", spinStates[i%len(spinStates)])
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()

	result, err := deploy.Run(opts)
	if err != nil {
		fmt.Printf("❌ Deployment failed: %v\n", err)
		os.Exit(1)
	}

	// Success output
	fmt.Println("✅ Deployment successful!")
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
