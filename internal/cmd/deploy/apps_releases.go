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

// DIB-809: every deploy pushes one immutable dep_… image and the registry
// keeps the newest ones. `apps releases` shows them; `apps rollback` points
// the app at one of them again without a build — the way back when a deploy
// went wrong or the build service is down.

var appsReleasesCmd = &cobra.Command{
	Use:   "releases <alias>",
	Short: "List the app's saved releases (images you can roll back to)",
	Long: `List the releases the platform still holds for an app: one immutable
image per successful deploy (dep_… id), newest first, with its digest, when it
was deployed, and which one is running.

A release marked "gone" was swept by registry retention and cannot be rolled
back to. "config" says the platform remembers the release's port, env and
resources — what recreating a missing deployment needs.

Examples:
  dibbla apps releases myapp
  dibbla apps releases myapp --json`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsReleases,
}

var appsRollbackCmd = &cobra.Command{
	Use:   "rollback <alias>",
	Short: "Roll the app back to an earlier release without a build",
	Long: `Switch a running app to an earlier release's image. Nothing is built:
the image already exists in the registry, so the rollback takes a rolling
update — the app answers with the earlier version within about a minute.

Without --to, the previous release is used (the newest one that is not
running). With --to <dep-id>, that release; see 'dibbla apps releases'.

If the app's deployment is missing (a force deploy removed it and the build
failed), rollback recreates it from the release's image with the last known
configuration — port, env, resources — the platform saved for it.

Env, resources and the login gate are inherited from the running app, exactly
like 'dibbla deploy --update' with no flags. Secrets are untouched.

Examples:
  dibbla apps rollback myapp                 # previous release
  dibbla apps rollback myapp --to dep_k3f9a  # a specific release
  dibbla apps rollback myapp --yes           # no confirmation prompt`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsRollback,
}

var (
	releasesJSON bool
	rollbackTo   string
	rollbackYes  bool
	rollbackJSON bool
)

func init() {
	appsCmd.AddCommand(appsReleasesCmd)
	appsCmd.AddCommand(appsRollbackCmd)
	appsReleasesCmd.Flags().BoolVar(&releasesJSON, "json", false, "Print the raw API document")
	appsRollbackCmd.Flags().StringVar(&rollbackTo, "to", "", "Release to roll back to (dep_… id from 'apps releases'); default: the previous release")
	appsRollbackCmd.Flags().BoolVarP(&rollbackYes, "yes", "y", false, "Skip confirmation prompt")
	appsRollbackCmd.Flags().BoolVar(&rollbackJSON, "json", false, "Print the JSON response body")
}

func runAppsReleases(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsReleasesCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], releasesJSON))
}

// runAppsReleasesCore is the testable inner implementation of `apps releases`.
func runAppsReleasesCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	doc, raw, err := apps.ListReleases(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "releases", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	printReleases(stdout, doc)
	return 0
}

func printReleases(stdout io.Writer, doc *apps.ReleasesResponse) {
	fmt.Fprintf(stdout, "%s %s — %d release(s)\n", platform.Icon("📦", "[APP]"), doc.Alias, len(doc.Releases))
	if !doc.DeploymentExists {
		fmt.Fprintf(stdout, "   %s the deployment is missing; 'dibbla apps rollback %s --to <dep-id>' recreates it from a release\n",
			platform.Icon("⚠️", "[!]"), doc.Alias)
	}
	if !doc.RegistryReachable {
		fmt.Fprintf(stdout, "   %s registry unreachable — availability could not be checked\n", platform.Icon("⚠️", "[!]"))
	}
	if len(doc.Releases) == 0 {
		fmt.Fprintln(stdout, "   (no releases yet — deploy once and they will appear here)")
		return
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "   %-16s %-20s %-16s %-10s %s\n", "RELEASE", "DEPLOYED", "DIGEST", "STATE", "AUTHOR")
	fmt.Fprintf(stdout, "   %s\n", strings.Repeat("-", 80))
	for _, r := range doc.Releases {
		when := "-"
		if r.CreatedAt != nil {
			when = r.CreatedAt.Local().Format("2006-01-02 15:04:05")
		}
		digest := "-"
		if r.Digest != "" {
			digest = shortDigest(r.Digest)
		}
		state := "available"
		switch {
		case r.Running:
			state = "running"
		case !r.Available:
			state = "gone"
		}
		if r.HasConfig && state != "gone" {
			state += "+config"
		}
		fmt.Fprintf(stdout, "   %-16s %-20s %-16s %-10s %s\n", r.DeploymentID, when, digest, state, orDash(r.AuthorEmail))
	}
	if doc.PreviousDeploymentID != "" {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "   Roll back: dibbla apps rollback %s              (→ %s)\n", doc.Alias, doc.PreviousDeploymentID)
		fmt.Fprintf(stdout, "              dibbla apps rollback %s --to <dep-id>  (any release above)\n", doc.Alias)
	}
}

// shortDigest trims "sha256:abcdef…" to its first 12 hex characters, the
// way container tooling prints it.
func shortDigest(d string) string {
	if i := strings.Index(d, ":"); i >= 0 {
		d = d[i+1:]
	}
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func runAppsRollback(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsRollbackCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], rollbackTo, rollbackYes, rollbackJSON, askConfirm))
}

// runAppsRollbackCore is the testable inner implementation of `apps rollback`.
// confirm is the prompt; --yes skips it. Nothing is requested when the alias
// or --to is malformed, or when the user declines.
func runAppsRollbackCore(stdout, stderr io.Writer, apiURL, apiToken, alias, to string, yes, jsonOut bool, confirm func(string) (bool, error)) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	to = strings.TrimSpace(to)
	if to != "" && !strings.HasPrefix(to, "dep_") {
		fmt.Fprintf(stderr, "%s --to must be a release id (dep_…); run 'dibbla apps releases %s' to see them\n",
			platform.Icon("❌", "[X]"), alias)
		return 5
	}
	if !yes {
		target := "the previous release"
		if to != "" {
			target = "release " + to
		}
		ok, err := confirm(fmt.Sprintf("Roll %s back to %s (rolling update, no build)?", alias, target))
		if err != nil {
			return refuseUnconfirmable(stderr, "apps rollback")
		}
		if !ok {
			fmt.Fprintln(stdout, "Cancelled.")
			return 0
		}
	}

	res, raw, err := apps.Rollback(apiURL, apiToken, alias, to)
	if err != nil {
		var statusErr *apps.StatusError
		if errors.As(err, &statusErr) {
			switch statusErr.Code {
			case "RELEASE_GONE":
				fmt.Fprintf(stderr, "%s rollback %s failed: %v\n", platform.Icon("❌", "[X]"), alias, statusErr)
				fmt.Fprintf(stderr, "  hint: that release's image was removed by registry retention; pick one still listed by 'dibbla apps releases %s'.\n", alias)
				return statusErr.ExitCode()
			case "RELEASE_NOT_FOUND":
				fmt.Fprintf(stderr, "%s rollback %s failed: %v\n", platform.Icon("❌", "[X]"), alias, statusErr)
				fmt.Fprintf(stderr, "  hint: run 'dibbla apps releases %s' to see the releases you can roll back to.\n", alias)
				return statusErr.ExitCode()
			case "RELEASE_CONFIG_UNKNOWN", "ROLLBACK_UNSUPPORTED":
				fmt.Fprintf(stderr, "%s rollback %s failed: %v\n", platform.Icon("❌", "[X]"), alias, statusErr)
				fmt.Fprintf(stderr, "  hint: 'dibbla deploy' from the source you want brings the app back.\n")
				return statusErr.ExitCode()
			}
		}
		return reportAppError(stderr, "rollback", alias, err)
	}

	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	switch res.Status {
	case "unchanged":
		fmt.Fprintf(stdout, "%s %s already runs %s — nothing to do\n", platform.Icon("✅", "[OK]"), alias, res.DeploymentID)
	default:
		verb := "rolled back to"
		if res.Recreated {
			verb = "recreated from release"
		}
		fmt.Fprintf(stdout, "%s %s %s %s (no build)\n", platform.Icon("✅", "[OK]"), alias, verb, res.DeploymentID)
		if res.PreviousDeploymentID != "" {
			fmt.Fprintf(stdout, "   was:   %s\n", res.PreviousDeploymentID)
		}
		fmt.Fprintf(stdout, "   image: %s\n", res.Image)
		fmt.Fprintf(stdout, "   The rolling update is in progress; the app answers with this version within about a minute.\n")
		fmt.Fprintf(stdout, "   Check: dibbla apps get %s\n", alias)
	}
	return 0
}
