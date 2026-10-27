package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// `dibbla deploy cancel <alias>` (DIB-1616): stop the deploy in progress for
// an app and free its deploy lock. Before this nothing in the CLI called the
// cancel endpoint, so a deploy whose connection had dropped — still running
// on the server, still holding the app — could only be waited out, and every
// retry answered DEPLOY_IN_PROGRESS.

var deployCancelCmd = &cobra.Command{
	Use:   "cancel <alias>",
	Short: "Stop the deploy in progress for an app",
	Long: `Stop the deploy in progress for an app and free it for the next deploy.

A deploy keeps running on the server when the connection to it drops; until
it ends, another deploy of the app answers DEPLOY_IN_PROGRESS. This stops it.
Objects the deploy already applied stay applied.

Examples:
  dibbla deploy cancel myapp`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runDeployCancel(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0]))
	},
}

func init() {
	deployCmd.AddCommand(deployCancelCmd)
}

func runDeployCancel(stdout, stderr io.Writer, apiURL, apiToken, alias string) int {
	alias = strings.TrimSpace(alias)
	out, err := apps.CancelDeploy(apiURL, apiToken, alias)
	if err != nil {
		fmt.Fprintf(stderr, "%s deploy cancel %s failed: %v\n", platform.Icon("❌", "[X]"), alias, err)
		var statusErr *apps.StatusError
		if errors.As(err, &statusErr) {
			return statusErr.ExitCode()
		}
		return 1
	}
	switch {
	case !out.Cancelled:
		fmt.Fprintf(stdout, "No deploy in progress for %s.\n", alias)
	case out.Accepted:
		fmt.Fprintf(stdout, "%s Cancel requested for deploy %s of %s; it stops within seconds.\n", platform.Icon("🛑", "[STOP]"), out.DeploymentID, alias)
	default:
		fmt.Fprintf(stdout, "%s Cancelled deploy %s of %s.\n", platform.Icon("🛑", "[STOP]"), out.DeploymentID, alias)
	}
	return 0
}
