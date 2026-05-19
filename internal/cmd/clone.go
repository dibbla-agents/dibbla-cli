package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
)

var (
	cloneRef  string
	cloneInto string
)

var cloneCmd = &cobra.Command{
	Use:   "clone <app> | <org>/<app>",
	Short: "Clone the Dibbla-managed git repo for a deployed app",
	Long: `Clone the Dibbla-managed version-control repo for one of your deployed apps.

Authentication reuses the token from "dibbla login" or the DIBBLA_API_TOKEN env
var — the same token you already use for "dibbla deploy". The token is passed
to git via an in-memory http.extraHeader config, so it never lands in
~/.git-credentials or your shell history.

Only Dibbla-authored commits from your deploys live in this repo. Pushing is
rejected by the platform — fork to GitHub or GitLab if you want to share
changes.

Examples:
  dibbla clone my-app
  dibbla clone my-app --into ./checkout
  dibbla clone my-app --ref abc1234
  dibbla clone dibbla/faq-bot           # org prefix accepted but optional;
                                        #   org is derived from the token.`,
	Args: cobra.ExactArgs(1),
	Run:  runClone,
}

func init() {
	cloneCmd.Flags().StringVar(&cloneRef, "ref", "", "Commit SHA to check out after clone (default: latest)")
	cloneCmd.Flags().StringVar(&cloneInto, "into", "", "Destination directory (default: ./<app>)")
	rootCmd.AddCommand(cloneCmd)
}

func runClone(cmd *cobra.Command, args []string) {
	input := strings.TrimSpace(args[0])
	_, alias := splitOrgApp(input)
	if alias == "" {
		fmt.Printf("%s Error: app name is required\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Printf("%s Error: API token is required. Run 'dibbla login' or set DIBBLA_API_TOKEN.\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	fmt.Printf("%s Resolving clone URL for %s...\n", platform.Icon("🔎", "[?]"), alias)
	info, err := vcs.GetInfo(cfg.APIURL, cfg.APIToken, alias)
	if err != nil {
		var apiErr *vcs.APIError
		if errors.As(err, &apiErr) {
			fmt.Printf("%s %s\n", platform.Icon("❌", "[X]"), describeCloneAPIError(apiErr))
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	if info.CloneURL == "" {
		fmt.Printf("%s Error: server did not return a clone URL. Is version control enabled for this environment?\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}
	if info.LatestSHA == "" {
		fmt.Printf("%s Nothing to clone: the app has no deploy-written commits yet.\n", platform.Icon("⚠️", "[!]"))
		os.Exit(0)
	}

	dest := strings.TrimSpace(cloneInto)
	if dest == "" {
		dest = filepath.Base(strings.TrimSuffix(info.CloneURL, ".git"))
	}

	if err := runGitClone(info.CloneURL, cfg.APIToken, dest); err != nil {
		fmt.Printf("%s git clone failed: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if cloneRef != "" {
		if err := runGitCheckout(dest, cloneRef); err != nil {
			fmt.Printf("%s git checkout failed: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
	}

	fmt.Printf("%s Cloned to %s\n", platform.Icon("✅", "[OK]"), dest)
	if cloneRef != "" {
		fmt.Printf("   checked out %s\n", cloneRef)
	} else if info.LatestCommit != nil {
		fmt.Printf("   latest: %s  %s\n", info.LatestCommit.ShortSHA, info.LatestCommit.Subject)
	}
}

// splitOrgApp accepts either "app" or "org/app" and returns (org, app).
// The org is informational only; the server picks it from the auth token.
func splitOrgApp(s string) (org, app string) {
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// runGitClone shells out to git with the token injected via
// `-c http.extraHeader=Authorization: Bearer <token>`. Using -c (rather than
// embedding the token in the URL) keeps it out of .git/config, the remote,
// and the process arg list captured in some shell histories.
//
// Note: -c does appear in /proc/<pid>/cmdline briefly. That's an acceptable
// tradeoff vs. the URL-embedded alternative, which would also land in
// .git/config permanently.
func runGitClone(cloneURL, token, dest string) error {
	args := []string{
		"-c", "http.extraHeader=Authorization: Bearer " + token,
		"clone", "--quiet", cloneURL, dest,
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runGitCheckout(dir, ref string) error {
	cmd := exec.Command("git", "-C", dir, "checkout", "--quiet", ref)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// describeCloneAPIError turns server error payloads into a single-line,
// user-actionable message. Keeps the switch narrow — server-side strings
// may change, so we lean on the status code.
func describeCloneAPIError(err *vcs.APIError) string {
	switch err.StatusCode {
	case 401:
		return "Authentication failed. Your token may be invalid or expired. Try 'dibbla login' again."
	case 403:
		return "Access denied."
	case 404:
		return "App not found in your org, or version control is not enabled for this environment."
	default:
		return fmt.Sprintf("Error (HTTP %d): %s", err.StatusCode, err.Body)
	}
}
