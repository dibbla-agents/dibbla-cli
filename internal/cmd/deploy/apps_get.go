package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
	"github.com/spf13/cobra"
)

var appsGetCmd = &cobra.Command{
	Use:   "get <alias>",
	Short: "Show one deployed application",
	Long: `Show the deployment record for one app: URL, status, the git commit it
runs, replicas, size, health check, login policy and — for multi-service
deployments — the per-service breakdown.

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
	if dep.CommitSHA != "" {
		// The full sha: it is what `dibbla clone` / `git log` show, and what
		// an agent pastes into a `git diff`. Short forms are for pills.
		fmt.Fprintf(stdout, "   Commit:   %s\n", dep.CommitSHA)
		printMainBehind(stdout, apiURL, apiToken, alias, dep.CommitSHA)
	}
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

// printMainBehind says when main is not what runs (DIB-903): a push whose
// deploy failed leaves main ahead of the running commit, and the gap must be
// explained where the running commit is shown. Best effort — an app without
// version control, or an info endpoint that is down, prints nothing extra.
func printMainBehind(stdout io.Writer, apiURL, apiToken, alias, running string) {
	info, err := vcs.GetInfo(apiURL, apiToken, alias)
	if err != nil || info.LatestSHA == "" || info.LatestSHA == running {
		return
	}
	fmt.Fprintf(stdout, "   Main:     %s — NOT running\n", info.LatestSHA)
	switch {
	case info.MainDeploy == nil:
		fmt.Fprintf(stdout, "             no deploy of this commit was started; run `dibbla deploy --update` from a clone\n")
	case info.MainDeploy.Phase == "failed":
		fmt.Fprintf(stdout, "             %s the deploy of main failed: %s — %s\n", platform.Icon("❌", "[X]"), info.MainDeploy.FailureCode, info.MainDeploy.FailureSummary)
		fmt.Fprintf(stdout, "             fix it with a new commit and push again; details: dibbla deploy status %s\n", info.MainDeploy.OperationID)
	case info.MainDeploy.Phase == "cancelled":
		fmt.Fprintf(stdout, "             the deploy of main was cancelled; push a new commit or run `dibbla deploy --update`\n")
	default:
		fmt.Fprintf(stdout, "             the deploy of main is %s: dibbla deploy status %s --follow\n", info.MainDeploy.Phase, info.MainDeploy.OperationID)
	}
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
		if statusErr.Status == 404 && (statusErr.Code == "" || statusErr.Code == "NOT_FOUND") {
			fmt.Fprintln(stderr, "  hint: run 'dibbla apps list' to see available aliases.")
		}
		return statusErr.ExitCode()
	}
	fmt.Fprintf(stderr, "%s %s %s failed: %v\n", platform.Icon("❌", "[X]"), verb, alias, err)
	return 1
}
