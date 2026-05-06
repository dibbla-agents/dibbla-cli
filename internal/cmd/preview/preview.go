// Package preview implements `dibbla preview`, the server-authoritative
// dry-run for a deploy. It uploads the archive (just like dibbla deploy
// would), but the server runs only manifest extract/parse/resolve/quota and
// returns the resolved shape — no build, no apply.
//
// Useful in CI before merging, in pre-commit hooks, or when sanity-checking
// env-aware fields ahead of a real deploy.
package preview

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	previewAlias     string
	previewTargetEnv string
	previewProfiles  []string
	previewNoPublic  bool
	previewPort      string
	previewJSON      bool
)

var previewCmd = &cobra.Command{
	Use:   "preview [path]",
	Short: "Preview what a deploy would do (no build, no apply)",
	Long: `Upload the archive and let the server resolve the manifest, run quota,
and report the shape of what would be deployed — without building or
applying anything.

Use this when:
  - You want to see how env-aware fields resolve for a target env.
  - You want to confirm which services would be deployed under a profile.
  - You want a quota check before a long build kicks off.
  - You want to verify a manifest without a real deploy slot.

For local-only schema checks (no network), use 'dibbla manifest validate'.

Examples:
  dibbla preview                              # ./, defaults to env=prod
  dibbla preview ./myapp --target-env staging
  dibbla preview --profile mailcatcher --profile metrics
  dibbla preview --no-public                  # worker-only is OK
  dibbla preview --json                       # machine-readable`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runPreview(os.Stdout, os.Stderr, args))
	},
}

// Register attaches the preview command to the given root.
func Register(root *cobra.Command) {
	root.AddCommand(previewCmd)
}

func init() {
	previewCmd.Flags().StringVarP(&previewAlias, "alias", "a", "", "Custom alias name (default: directory name)")
	previewCmd.Flags().StringVar(&previewTargetEnv, "target-env", "", "Manifest env name to resolve (defaults to 'prod' server-side)")
	previewCmd.Flags().StringArrayVar(&previewProfiles, "profile", nil, "Activate a manifest profile (repeatable)")
	previewCmd.Flags().BoolVar(&previewNoPublic, "no-public", false, "Allow preview with no public:true service")
	previewCmd.Flags().StringVar(&previewPort, "port", "", "Forwarded as 'port' field — used by the no-manifest synthesizer")
	previewCmd.Flags().BoolVar(&previewJSON, "json", false, "Emit raw PreviewResponse JSON instead of human text")
}

// runPreview is the testable entry point. Returns exit code: 0 if valid,
// 1 if the server reported errors or the request failed.
func runPreview(stdout, stderr io.Writer, args []string) int {
	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Fprintf(stderr, "%s API token is required (run 'dibbla login' or set DIBBLA_API_TOKEN)\n",
			platform.Icon("❌", "[X]"))
		return 1
	}

	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	opts := deploypkg.PreviewOptions{
		APIURL:    cfg.APIURL,
		APIToken:  cfg.APIToken,
		Path:      path,
		Alias:     previewAlias,
		TargetEnv: previewTargetEnv,
		Profiles:  previewProfiles,
		NoPublic:  previewNoPublic,
		Port:      previewPort,
	}

	resp, err := deploypkg.Preview(opts)
	if err != nil {
		fmt.Fprintf(stderr, "%s preview failed: %v\n", platform.Icon("❌", "[X]"), err)
		return 1
	}

	if previewJSON {
		_ = json.NewEncoder(stdout).Encode(resp)
		if !resp.Valid {
			return 1
		}
		return 0
	}

	if !resp.Valid {
		fmt.Fprintf(stderr, "%s preview reports manifest is invalid\n", platform.Icon("✗", "[X]"))
		for _, e := range resp.Errors {
			if e.Path != "" {
				fmt.Fprintf(stderr, "  %s: %s (%s)\n", e.Path, e.Detail, e.Code)
			} else {
				fmt.Fprintf(stderr, "  %s (%s)\n", e.Detail, e.Code)
			}
		}
		return 1
	}

	fmt.Fprintf(stdout, "%s preview valid\n", platform.Icon("✓", "[OK]"))
	fmt.Fprintf(stdout, "  alias:  %s\n", resp.Alias)
	if resp.Env != "" {
		fmt.Fprintf(stdout, "  env:    %s\n", resp.Env)
	}
	if resp.PublicService != "" {
		fmt.Fprintf(stdout, "  public: %s\n", resp.PublicService)
	}
	if len(resp.ActiveServices) > 0 {
		fmt.Fprintf(stdout, "  active services (%d):\n", len(resp.ActiveServices))
		for _, s := range resp.ActiveServices {
			marker := ""
			if s.IsPublic {
				marker = " (public)"
			}
			source := "image"
			if s.IsBuilt {
				source = "build"
			}
			fmt.Fprintf(stdout, "    - %-20s %s, replicas=%d%s\n", s.Name, source, s.Replicas, marker)
		}
	}
	if len(resp.SkippedServices) > 0 {
		fmt.Fprintf(stdout, "  skipped (%d):\n", len(resp.SkippedServices))
		for _, sk := range resp.SkippedServices {
			fmt.Fprintf(stdout, "    - %-20s reason: %s\n", sk.Name, sk.Reason)
		}
	}
	if len(resp.Warnings) > 0 {
		fmt.Fprintln(stdout, "  warnings:")
		for _, w := range resp.Warnings {
			fmt.Fprintf(stdout, "    - %s\n", w)
		}
	}
	return 0
}
