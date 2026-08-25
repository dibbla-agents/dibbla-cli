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

var appsGetCmd = &cobra.Command{
	Use:   "get <alias>",
	Short: "Show one deployed application",
	Long: `Show the deployment record for one app: URL, status, replicas, size,
health check, login policy and — for multi-service deployments — the
per-service breakdown.

Referenced by ` + "`dibbla logs --pod-stream`" + ` errors as the way to check
an app's services without the console.

Examples:
  dibbla apps get myapp
  dibbla apps get myapp --json | jq .`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsGet,
}

var appsGetJSON bool

func init() {
	appsGetCmd.Flags().BoolVar(&appsGetJSON, "json", false, "Print the raw API document")
}

func runAppsGet(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsGetCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], appsGetJSON))
}

// runAppsGetCore is the testable inner implementation of `apps get`.
// Returns the exit code. Side effects: writes to the given writers and one
// HTTP GET.
func runAppsGetCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		fmt.Fprintf(stderr, "%s alias %q does not match %s\n",
			platform.Icon("❌", "[X]"), alias, apps.AliasRe.String())
		return 5
	}

	dep, raw, err := apps.GetApp(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "get", alias, err)
	}

	if jsonOut {
		// Emit the server document verbatim — the machine contract mirrors
		// the API rather than a CLI-shaped subset of it.
		fmt.Fprintln(stdout, string(raw))
		return 0
	}

	fmt.Fprintf(stdout, "%s %s — %s\n", platform.Icon("📦", "[APP]"), dep.Alias, dep.URL)
	fmt.Fprintf(stdout, "   Status:  %s\n", dep.Status)
	if dep.DeployedAt != nil {
		fmt.Fprintf(stdout, "   Deployed: %s\n", dep.DeployedAt.Local().Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(stdout, "   Updated:  %s\n", dep.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	if dep.Replicas != nil {
		fmt.Fprintf(stdout, "   Replicas: %d\n", *dep.Replicas)
	}
	if dep.CPU != "" || dep.Memory != "" {
		fmt.Fprintf(stdout, "   Size:     %s CPU / %s memory\n", orDash(dep.CPU), orDash(dep.Memory))
	}
	if dep.HealthCheck != nil {
		fmt.Fprintf(stdout, "   Health:   %s (%dms)\n", dep.HealthCheck.Status, dep.HealthCheck.ResponseTimeMs)
	}
	if dep.RequireLogin {
		fmt.Fprintf(stdout, "   Login:    required (%s)\n", orDash(dep.AppAccessPolicy))
	} else {
		fmt.Fprintf(stdout, "   Login:    public\n")
	}
	if dep.Error != "" {
		fmt.Fprintf(stdout, "   Error:    %s\n", dep.Error)
	}
	if len(dep.Services) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "   Services (%d):\n", len(dep.Services))
		for _, svc := range dep.Services {
			state := svc.Status
			if state == "" {
				state = string(dep.Status)
			}
			fmt.Fprintf(stdout, "     %-20s %-12s %d/%d ready", svc.Name, state, svc.ReadyReplicas, svc.Replicas)
			if svc.Stateful {
				fmt.Fprintf(stdout, " (stateful)")
			}
			fmt.Fprintln(stdout)
		}
	}
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// reportAppError prints one transport failure with the server's stable code
// and prose detail, plus the alias context, and returns the ladder exit code.
// Shared by `apps get` and the `apps checks` family.
func reportAppError(stderr io.Writer, verb, alias string, err error) int {
	var statusErr *apps.StatusError
	if errors.As(err, &statusErr) {
		fmt.Fprintf(stderr, "%s %s %s failed: %v\n", platform.Icon("❌", "[X]"), verb, alias, statusErr)
		// "Check your aliases" only helps when the app is what went missing.
		// A 404 with a server code naming another cause (the org has the
		// checks capability disabled, an endpoint is not deployed yet) would
		// make the hint a wrong answer.
		if statusErr.Status == 404 && (statusErr.Code == "" || strings.Contains(statusErr.Code, "NOT_FOUND")) {
			fmt.Fprintln(stderr, "  hint: run 'dibbla apps list' to see available aliases.")
		}
		return statusErr.ExitCode()
	}
	fmt.Fprintf(stderr, "%s %s %s failed: %v\n", platform.Icon("❌", "[X]"), verb, alias, err)
	return 1
}
