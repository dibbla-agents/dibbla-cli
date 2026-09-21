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
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
	"github.com/spf13/cobra"
)

var appsGetCmd = &cobra.Command{
	Use:     "get <alias>",
	Aliases: []string{"card"},
	Short:   "Show one deployed application (its card)",
	Long: `Show the deployment record for one app: URL, status, the git commit it
runs, replicas, size, health check, login policy, the security section —
guardrails review and which version it applies to, the build-time scan and
when its findings last changed, the maintenance agent and whether anything
changed since it last ran, proposals waiting — and, for multi-service
deployments, the per-service breakdown.

` + "`dibbla apps card <alias>`" + ` is the same command: the app as the console and
the connector's app card show it.

Referenced by ` + "`dibbla logs --pod-stream`" + ` errors as the way to check
an app's services without the console.

Examples:
  dibbla apps get myapp
  dibbla apps card myapp
  dibbla apps get myapp --review        # print the deployed REVIEW.md
  dibbla apps get myapp --json | jq .`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsGet,
}

var (
	appsGetJSON   bool
	appsGetReview bool
)

func init() {
	appsGetCmd.Flags().BoolVar(&appsGetJSON, "json", false, "Print the raw API document")
	appsGetCmd.Flags().BoolVar(&appsGetReview, "review", false, "Print the REVIEW.md the running app was deployed with, and nothing else")
}

func runAppsGet(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsGetCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], appsGetJSON, appsGetReview))
}

// runAppsGetCore is the testable inner implementation of `apps get`.
// Returns the exit code. Side effects: writes to the given writers and one
// HTTP GET.
func runAppsGetCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut, reviewOut bool) int {
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
	if reviewOut {
		// The REVIEW.md as deployed — the "link to REVIEW.md" of the card.
		if dep.ReviewBody == "" {
			fmt.Fprintf(stderr, "%s %s was deployed without a REVIEW.md\n", platform.Icon("❌", "[X]"), alias)
			return 1
		}
		fmt.Fprintln(stdout, dep.ReviewBody)
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
	printSecurity(stdout, alias, dep)
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

// printSecurity is the card's security section (DIB-965): review, scan,
// agent — each with WHEN and FOR WHICH VERSION, because "Ok" without a
// version is what the old review dot said. Nothing is printed for a server
// that predates the section (nil), so an old server does not read as an
// app with no review.
func printSecurity(stdout io.Writer, alias string, dep *apps.Deployment) {
	sec := dep.Security
	if sec == nil {
		return
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "   Security:")
	if r := sec.Review; r != nil {
		switch {
		case !r.Present:
			fmt.Fprintf(stdout, "     Review:  %s none — no REVIEW.md was deployed\n", platform.Icon("❌", "[X]"))
		default:
			line := reviewWord(r.Status)
			if r.CommitSHA != "" {
				line += " — for version " + shortSHA(r.CommitSHA)
			}
			if r.ReviewedAt != nil {
				line += ", written " + r.ReviewedAt.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(stdout, "     Review:  %s\n", line)
			if r.Summary != "" {
				fmt.Fprintf(stdout, "              %s\n", r.Summary)
			}
			if r.CodeChangedSince {
				fmt.Fprintf(stdout, "              %s the running version (%s) is newer than the review\n", platform.Icon("⚠️", "[!]"), shortSHA(dep.CommitSHA))
			}
			if dep.ReviewBody != "" {
				fmt.Fprintf(stdout, "              REVIEW.md: dibbla apps get %s --review\n", alias)
			}
		}
	}
	if s := sec.Scan; s != nil {
		fmt.Fprintf(stdout, "     Scan:    %s\n", scanLine(s))
		if s.Status != "running" && s.Status != "failed" {
			if s.FindingsChangedAt != nil {
				fmt.Fprintf(stdout, "              findings last changed %s\n", s.FindingsChangedAt.Local().Format("2006-01-02 15:04"))
			}
			if at := s.CompletedAt; at != nil {
				fmt.Fprintf(stdout, "              scanned %s (%d image(s), %d packages)\n", at.Local().Format("2006-01-02 15:04"), s.Images, s.Packages)
			}
		}
		if len(s.Errors) > 0 {
			fmt.Fprintf(stdout, "              %d part(s) of the scan did not run\n", len(s.Errors))
		}
	} else {
		fmt.Fprintf(stdout, "     Scan:    this version has not been scanned\n")
	}
	if m := sec.Maintenance; m != nil {
		fmt.Fprintf(stdout, "     Agent:   %s\n", agentLine(m))
		if m.LastRunAt != nil {
			fmt.Fprintf(stdout, "              last run %s", m.LastRunAt.Local().Format("2006-01-02 15:04"))
			if m.LastRunCode != "" {
				fmt.Fprintf(stdout, " (%s)", m.LastRunCode)
			}
			fmt.Fprintln(stdout)
		}
		if m.PendingProposals > 0 {
			fmt.Fprintf(stdout, "              %d proposal(s) waiting for a decision: dibbla apps proposals list %s\n", m.PendingProposals, alias)
		}
	}
}

func reviewWord(status string) string {
	switch status {
	case "Ok":
		return platform.Icon("✅", "[OK]") + " OK"
	case "Warnings":
		return platform.Icon("⚠️", "[!]") + " warnings"
	case "Critical":
		return platform.Icon("❌", "[X]") + " blockers found"
	default:
		return "present"
	}
}

// scanLine is the tally in words: secrets first (a leaked key is never
// "just" a CVE), then the worst severities; medium and below are counted
// but never alarm on their own.
func scanLine(s *apps.SecurityScan) string {
	switch s.Status {
	case "running":
		return "running…"
	case "failed":
		return "could not run"
	}
	v := s.Vulnerabilities
	var parts []string
	if s.Secrets > 0 {
		parts = append(parts, fmt.Sprintf("%d leaked secret(s)", s.Secrets))
	}
	if v.Critical > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", v.Critical))
	}
	if v.High > 0 {
		parts = append(parts, fmt.Sprintf("%d high", v.High))
	}
	if v.Medium > 0 {
		parts = append(parts, fmt.Sprintf("%d medium", v.Medium))
	}
	if v.Low > 0 {
		parts = append(parts, fmt.Sprintf("%d low", v.Low))
	}
	if rest := v.Negligible + v.Unknown; rest > 0 {
		parts = append(parts, fmt.Sprintf("%d other", rest))
	}
	if len(parts) == 0 {
		if s.Status == "partial" {
			return platform.Icon("✅", "[OK]") + " clean (partial scan)"
		}
		return platform.Icon("✅", "[OK]") + " clean"
	}
	icon := platform.Icon("⚠️", "[!]")
	if s.Secrets > 0 || v.Critical > 0 || v.High > 0 {
		icon = platform.Icon("❌", "[X]")
	}
	return icon + " " + strings.Join(parts, " · ")
}

// agentLine reads a quiet agent as quiet on purpose, not as absent.
func agentLine(m *apps.SecurityMaintenance) string {
	switch {
	case !m.Configured:
		return "not set up for this organization"
	case !m.Enabled:
		return "off for this app"
	case m.LastRunAt == nil:
		return "on — has not run for this app yet"
	case m.ChangedSinceLastRun:
		return "on — something changed since it last looked"
	default:
		return "on — nothing has changed since it last looked"
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
