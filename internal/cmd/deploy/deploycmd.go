package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/spinner"
	"github.com/spf13/cobra"
)

var (
	deployForce        bool
	deployUpdate       bool
	deployAlias        string
	deployEnv          []string
	deployCPU          string
	deployMemory       string
	deployPort         string
	deployFavicon      string
	deployRequireLogin bool
	deployAccessPolicy string
	deployGoogleScopes []string
	deployMessage      string
)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an application to dibbla.com",
	Long: `Deploy a containerized application to the dibbla.com platform.

Your application will be available at https://<alias>.dibbla.com

Configuration:
  Run dibbla login to store credentials, or set DIBBLA_API_TOKEN (and optionally DIBBLA_API_URL) in your environment or .env file.

Examples:
  dibbla deploy              # Deploy current directory
  dibbla deploy ./myapp      # Deploy specific directory
  dibbla deploy --alias my-api  # Deploy with custom alias name
  dibbla deploy -m "feat: add /healthz endpoint"   # Set VCS commit subject
  dibbla deploy --update     # Rolling update (zero downtime)
  dibbla deploy --force      # Force redeploy existing alias (causes downtime)
  dibbla deploy --cpu 500m --memory 512Mi --port 3000
  dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
  dibbla deploy --favicon https://example.com/favicon.ico`,
	Args: cobra.MaximumNArgs(1),
	Run:  runDeploy,
}

func init() {
	deployCmd.Flags().BoolVarP(&deployForce, "force", "f", false, "Force redeploy if alias already exists (causes downtime)")
	deployCmd.Flags().BoolVarP(&deployUpdate, "update", "u", false, "Rolling update of existing deployment (zero downtime)")
	deployCmd.Flags().StringVarP(&deployAlias, "alias", "a", "", "Custom alias name (default: directory name)")
	deployCmd.Flags().StringArrayVarP(&deployEnv, "env", "e", nil, "Set env var KEY=value (repeatable)")
	deployCmd.Flags().StringVar(&deployCPU, "cpu", "", "CPU request (e.g. 500m)")
	deployCmd.Flags().StringVar(&deployMemory, "memory", "", "Memory request (e.g. 512Mi)")
	deployCmd.Flags().StringVar(&deployPort, "port", "", "Container port (e.g. 3000)")
	deployCmd.Flags().StringVar(&deployFavicon, "favicon", "", "Favicon URL (e.g. https://example.com/favicon.ico)")
	deployCmd.Flags().BoolVar(&deployRequireLogin, "require-login", false, "Require authentication to access the app")
	deployCmd.Flags().StringVar(&deployAccessPolicy, "access-policy", "", "Access policy: all_members or invite_only")
	deployCmd.Flags().StringArrayVar(&deployGoogleScopes, "google-scopes", nil, "Google OAuth scope URL (repeatable)")
	deployCmd.Flags().StringVarP(&deployMessage, "message", "m", "", "Deploy message, used as the VCS commit subject (e.g. \"fix: handle null user\")")
	deployCmd.MarkFlagsMutuallyExclusive("force", "update")
}

func runDeploy(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Dibbla Deploy\n", platform.Icon("🚀", ">>"))
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Printf("%s Error: Invalid path: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		fmt.Printf("%s Error: Directory not found: %s\n", platform.Icon("❌", "[X]"), absPath)
		os.Exit(1)
	}

	fmt.Printf("%s Deploying: %s\n", platform.Icon("📁", "[DIR]"), absPath)
	if deployAlias != "" {
		fmt.Printf("%s Alias: %s\n", platform.Icon("🏷️", "[TAG]"), deployAlias)
	}
	fmt.Printf("%s API: %s\n", platform.Icon("🌐", "[NET]"), cfg.APIURL)
	if deployUpdate {
		fmt.Printf("%s Update mode: rolling update with zero downtime\n", platform.Icon("🔄", "[UPD]"))
	}
	if deployForce {
		fmt.Printf("%s Force mode: will overwrite existing deployment\n", platform.Icon("⚠️", "[!]"))
	}
	fmt.Println()

	fmt.Printf("%s Creating archive...\n", platform.Icon("📦", "[PKG]"))

	opts := deploypkg.Options{
		APIURL:       cfg.APIURL,
		APIToken:     cfg.APIToken,
		Path:         path,
		Force:        deployForce,
		Update:       deployUpdate,
		Alias:        deployAlias,
		Env:          deployEnv,
		CPU:          deployCPU,
		Memory:       deployMemory,
		Port:         deployPort,
		FaviconURL:   deployFavicon,
		RequireLogin: deployRequireLogin,
		AccessPolicy: deployAccessPolicy,
		GoogleScopes: deployGoogleScopes,
		Message:      deployMessage,
	}

	action := "Deploying"
	if deployUpdate {
		action = "Updating"
	}
	fmt.Printf("%s Uploading and %s...\n", platform.Icon("☁️", "[CLOUD]"), strings.ToLower(action))
	fmt.Println()

	stop := spinner.Start(action, "\033[32m")

	result, err := deploypkg.Run(opts)
	stop()
	if err != nil {
		fmt.Printf("\r%s Deployment failed: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if deployUpdate {
		fmt.Printf("\r%s Update successful!\n", platform.Icon("✅", "[OK]"))
	} else {
		fmt.Printf("\r%s Deployment successful!\n", platform.Icon("✅", "[OK]"))
	}
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
