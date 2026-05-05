package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	isatty "github.com/mattn/go-isatty"
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
	deployQuiet        bool
	deployJSON         bool
	deployVerboseBuild bool
)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an application to dibbla.com",
	Long: `Deploy a containerized application to the dibbla.com platform.

Your application will be available at https://<alias>.dibbla.com

Symlinks: symlinks inside the deploy directory are followed and their content is
included as regular files in the archive. Symlinks whose target escapes the deploy
root (including absolute symlinks such as /etc/passwd) are skipped to prevent
accidentally packaging host files.

Configuration:
  Run dibbla login to store credentials, or set DIBBLA_API_TOKEN (and optionally DIBBLA_API_URL) in your environment or .env file.

Output modes:
  When stdout is a TTY, dibbla streams a live build view with per-step
  progress. In CI or when piped, it switches to ISO-timestamped log lines
  (no cursor moves, grep-friendly). --quiet collapses success to one line;
  --json emits a single structured object that scripts can parse with jq.
  On build failure --verbose-build asks the server to ship the full build
  log instead of relying on parsed compile diagnostics alone.

Examples:
  dibbla deploy              # Deploy current directory
  dibbla deploy ./myapp      # Deploy specific directory
  dibbla deploy --alias my-api  # Deploy with custom alias name
  dibbla deploy -m "feat: add /healthz endpoint"   # Set VCS commit subject
  dibbla deploy --update     # Rolling update (zero downtime)
  dibbla deploy --force      # Force redeploy existing alias (causes downtime)
  dibbla deploy --cpu 500m --memory 512Mi --port 3000
  dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
  dibbla deploy --favicon https://example.com/favicon.ico
  dibbla deploy --quiet      # Single-line success/failure (script-friendly)
  dibbla deploy --json       # Structured JSON output for jq / agents`,
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
	deployCmd.Flags().BoolVar(&deployQuiet, "quiet", false, "Suppress build progress; print one line on success/failure")
	deployCmd.Flags().BoolVar(&deployJSON, "json", false, "Emit a single structured JSON object on completion")
	deployCmd.Flags().BoolVar(&deployVerboseBuild, "verbose-build", false, "On build failure, request the full server build log instead of just the elided tail")
	deployCmd.MarkFlagsMutuallyExclusive("force", "update")
	deployCmd.MarkFlagsMutuallyExclusive("quiet", "json")
}

func runDeploy(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)

	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ invalid path: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "✗ directory not found: %s\n", absPath)
		os.Exit(1)
	}

	r := selectRenderer()

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
		VerboseBuild: deployVerboseBuild,
	}

	_, err = deploypkg.Run(opts, r)
	if err != nil && r == nil {
		// Legacy/no-renderer path — surface the raw error directly.
		fmt.Fprintf(os.Stderr, "✗ deploy failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(r.OnDone())
}

// selectRenderer picks an output renderer based on flags and stdout type.
// Order: --json > --quiet > TTY (interactive) > log (CI / piped).
// platform.IsCI is used as a belt-and-braces fallback so that explicit CI
// env vars force the log renderer even if isatty is fooled by an
// allocated pty (some CI runners do this).
func selectRenderer() render.Renderer {
	switch {
	case deployJSON:
		return render.NewJSON(os.Stdout)
	case deployQuiet:
		return render.NewQuiet(os.Stdout)
	case isatty.IsTerminal(os.Stdout.Fd()) && !platform.IsCI():
		return render.NewTTY(os.Stdout, true)
	default:
		return render.NewLog(os.Stdout, os.Stderr)
	}
}
