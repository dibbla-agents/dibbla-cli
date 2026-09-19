package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/gitcred"
	"github.com/dibbla-agents/dibbla-cli/internal/gitlink"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
)

var (
	cloneRef  string
	cloneInto string
	cloneYes  bool
)

var cloneCmd = &cobra.Command{
	Use:     "clone <app> | <org>/<app>",
	Aliases: []string{"link"},
	Short:   "Connect a folder to an app's Dibbla git repo (clone, or link the folder you are in)",
	Long: `Connect a local folder to the Dibbla-managed git repo of one of your apps.
Dibbla is the app's origin: every deploy is a commit on main, and a linked
folder fetches and pulls from it like any other remote.

The command looks at the destination first and does what fits, so you never
have to run git clone by hand:

  empty or missing folder        git clone — origin = Dibbla, main checked out
  files but no .git              cloned into ./<app>/ (or linked in place with --into .)
  a repo with no commits yet     remote added, main fetched and checked out
  a repo with its own history    files on disk are kept, history starts over from
                                 Dibbla's main; asks first (--yes for agents); the
                                 old commits stay on a pre-dibbla-* branch and
                                 git status shows disk vs Dibbla as changes to commit
  already linked to this app     git pull --ff-only

"dibbla link <app>" is the same command with --into . as the default: link the
folder you are standing in.

Authentication reuses the token from "dibbla login" or the DIBBLA_API_TOKEN env
var. git reads it through the "dibbla git-credential" helper that login
registers for Dibbla's git host, so it never lands in .git/config,
~/.git-credentials or your shell history.

Examples:
  dibbla clone my-app                   # → ./my-app
  dibbla clone my-app --into .          # this folder, whatever state it is in
  dibbla link my-app                    # same as --into .
  dibbla link my-app --yes              # no prompt when the folder has its own history
  dibbla clone my-app --ref abc1234     # fresh clone at an older deploy
  dibbla clone dibbla/faq-bot           # org prefix accepted but optional;
                                        #   org is derived from the token.`,
	Args: cobra.ExactArgs(1),
	Run:  runClone,
}

func init() {
	cloneCmd.Flags().StringVar(&cloneRef, "ref", "", "Commit SHA to check out after a fresh clone (default: latest)")
	cloneCmd.Flags().StringVar(&cloneInto, "into", "", "Destination directory (default: ./<app>; \"dibbla link\" defaults to .)")
	cloneCmd.Flags().BoolVarP(&cloneYes, "yes", "y", false, "Link a repo with its own history without asking")
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
	if err := preflight.RequireTool("git"); err != nil {
		fmt.Printf("%s %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s Resolving clone URL for %s...\n", platform.Icon("🔎", "[?]"), alias)
	info, err := vcs.GetInfo(cfg.APIURL, cfg.APIToken, alias)
	if err != nil {
		var apiErr *vcs.APIError
		if errors.As(err, &apiErr) {
			fmt.Printf("%s %s\n", platform.Icon("❌", "[X]"), describeCloneAPIError(apiErr, cfg))
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
	branch := info.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	// Registration is repeated here (idempotent) so a clone from a login made
	// before the helper existed, or from DIBBLA_API_TOKEN alone, still works.
	if err := gitcred.Register(cfg.APIURL); err != nil {
		fmt.Printf("%s register git credential helper: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	dest, explicit := resolveCloneDest(cmd, info.CloneURL)
	if err := connectFolder(dest, explicit, alias, branch, info, cloneRef, cloneYes, os.Stdout, os.Stderr); err != nil {
		fmt.Printf("%s %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
}

// resolveCloneDest picks the destination and whether the user chose it.
// `dibbla link` means "this folder"; `dibbla clone` without --into means
// ./<app> — unless the current folder is already linked to this very app, in
// which case the user plainly means "update what I have".
func resolveCloneDest(cmd *cobra.Command, cloneURL string) (dest string, explicit bool) {
	dest = strings.TrimSpace(cloneInto)
	if dest != "" {
		return dest, true
	}
	if cmd != nil && cmd.CalledAs() == "link" {
		return ".", true
	}
	if st, err := gitlink.Inspect(".", cloneURL); err == nil && st.Mode == gitlink.ModeLinked {
		return ".", true
	}
	return filepath.Base(strings.TrimSuffix(cloneURL, ".git")), false
}

// connectFolder does the mode-dependent work for clone/link. Split from
// runClone so tests can drive every folder state against a local bare repo
// without an API or os.Exit in the way.
func connectFolder(dest string, explicit bool, alias, branch string, info *vcs.Info, ref string, yes bool, stdout, stderr io.Writer) error {
	ok := platform.Icon("✅", "[OK]")
	st, err := gitlink.Inspect(dest, info.CloneURL)
	if err != nil {
		return err
	}
	shown := dest
	if abs, err := filepath.Abs(dest); err == nil && (dest == "." || dest == "./") {
		shown = abs
	}

	if ref != "" && st.Mode != gitlink.ModeMissing && st.Mode != gitlink.ModeEmpty {
		return fmt.Errorf("--ref only applies to a fresh clone; %s is %s. Link it first, then git checkout %s", shown, describeMode(st), ref)
	}

	switch st.Mode {
	case gitlink.ModeMissing, gitlink.ModeEmpty:
		if err := runGitClone(info.CloneURL, dest, stdout, stderr); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
		if ref != "" {
			if err := runGitCheckout(dest, ref, stdout, stderr); err != nil {
				return fmt.Errorf("git checkout failed: %w", err)
			}
		}
		fmt.Fprintf(stdout, "%s Cloned to %s  (origin = Dibbla, %s checked out)\n", ok, shown, branch)
		if !explicit {
			fmt.Fprintf(stdout, "   in a subfolder — pass --into . to use the current folder instead\n")
		}
		if ref != "" {
			fmt.Fprintf(stdout, "   checked out %s\n", ref)
		} else if info.LatestCommit != nil {
			fmt.Fprintf(stdout, "   latest: %s  %s\n", info.LatestCommit.ShortSHA, info.LatestCommit.Subject)
		}
		return nil

	case gitlink.ModeFiles:
		if !explicit {
			// ./<app> exists and has files but is not a repo: refuse rather
			// than silently linking a folder the user did not name.
			return fmt.Errorf("%s already exists and is not a git repo. Use --into <dir> for another location, or --into %s to link it in place", shown, dest)
		}
		fmt.Fprintf(stdout, "%s Linking %s to %s: files on disk are kept, files only Dibbla has are checked out.\n", platform.Icon("🔗", "[~]"), shown, alias)
		return finishLink(dest, shown, alias, branch, info, stdout, stderr)

	case gitlink.ModeRepoUnborn:
		if !explicit {
			return fmt.Errorf("%s already exists (a git repo with no commits). Use --into %s to link it, or --into <dir> for another location", shown, dest)
		}
		fmt.Fprintf(stdout, "%s Linking %s (a repo with no commits yet) to %s.\n", platform.Icon("🔗", "[~]"), shown, alias)
		return finishLink(dest, shown, alias, branch, info, stdout, stderr)

	case gitlink.ModeRepoHistory:
		if !explicit {
			return fmt.Errorf("%s already exists (a git repo with %d commits, no Dibbla remote). Use --into %s to link it, or --into <dir> for another location", shown, st.Commits, dest)
		}
		fmt.Fprintf(stdout, "%s %s is a git repo with %d commit(s) of its own and no Dibbla remote.\n", platform.Icon("⚠️", "[!]"), shown, st.Commits)
		fmt.Fprintf(stdout, "   Linking it to %s keeps every file on disk but starts the history over from\n", alias)
		fmt.Fprintf(stdout, "   Dibbla's %s: the local commits move to a pre-dibbla-* branch, and git status\n", branch)
		fmt.Fprintf(stdout, "   will show the difference between this folder and Dibbla as changes to commit.\n")
		fmt.Fprintf(stdout, "   Two histories are not merged.\n")
		if !confirmLink(yes, stderr) {
			return errors.New("not linked. Re-run with --yes to link without a prompt")
		}
		return finishLink(dest, shown, alias, branch, info, stdout, stderr)

	case gitlink.ModeLinked:
		if err := gitlink.Pull(dest, st.Remote, stderr); err != nil {
			return fmt.Errorf("%s is already linked to %s, but git pull --ff-only failed: %w\n   Local commits and Dibbla have diverged; see dibbla status", shown, alias, err)
		}
		fmt.Fprintf(stdout, "%s %s is already linked to %s — updated to Dibbla's %s (git pull --ff-only).\n", ok, shown, alias, branch)
		if info.LatestCommit != nil {
			fmt.Fprintf(stdout, "   latest: %s  %s\n", info.LatestCommit.ShortSHA, info.LatestCommit.Subject)
		}
		return nil
	}
	return fmt.Errorf("unexpected folder state %s", st.Mode)
}

func finishLink(dest, shown, alias, branch string, info *vcs.Info, stdout, stderr io.Writer) error {
	res, err := gitlink.Link(dest, info.CloneURL, branch, stderr)
	if err != nil {
		return fmt.Errorf("link failed: %w", err)
	}
	fmt.Fprintf(stdout, "%s Linked %s to %s  (remote %s = Dibbla, %s tracks %s/%s)\n", platform.Icon("✅", "[OK]"), shown, alias, res.Remote, branch, res.Remote, branch)
	if info.LatestCommit != nil {
		fmt.Fprintf(stdout, "   latest: %s  %s\n", info.LatestCommit.ShortSHA, info.LatestCommit.Subject)
	}
	if res.Restored > 0 {
		fmt.Fprintf(stdout, "   checked out %d file(s) from Dibbla that were not on disk\n", res.Restored)
	}
	if res.Backup != "" {
		fmt.Fprintf(stdout, "   previous local history kept on branch %s\n", res.Backup)
	}
	if res.Remote != "origin" {
		fmt.Fprintf(stdout, "   origin already pointed elsewhere, so Dibbla is the remote %q\n", res.Remote)
	}
	fmt.Fprintf(stdout, "   run git status to see what differs from Dibbla\n")
	return nil
}

func describeMode(st gitlink.State) string {
	switch st.Mode {
	case gitlink.ModeFiles:
		return "a folder with files"
	case gitlink.ModeRepoUnborn:
		return "a git repo with no commits"
	case gitlink.ModeRepoHistory:
		return fmt.Sprintf("a git repo with %d commit(s)", st.Commits)
	case gitlink.ModeLinked:
		return "already linked"
	}
	return st.Mode.String()
}

// confirmLink asks before a link that rewrites a folder's history. Agents
// pass --yes; a non-TTY without it is a refusal, never a silent yes.
func confirmLink(yes bool, stderr io.Writer) bool {
	if yes {
		return true
	}
	fi, _ := os.Stdin.Stat()
	if fi == nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		fmt.Fprintf(stderr, "Not a terminal — pass --yes to confirm.\n")
		return false
	}
	fmt.Fprintf(stderr, "Link this folder and start its history over from Dibbla? [y/N]: ")
	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// splitOrgApp accepts either "app" or "org/app" and returns (org, app).
// The org is informational only; the server picks it from the auth token.
func splitOrgApp(s string) (org, app string) {
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// runGitClone shells out to git. The token is not passed here at all: git asks
// the credential helper registered for Dibbla's git host, which reads the
// same login this command did. That keeps the token out of .git/config, the
// process arg list and /proc, and leaves the clone able to pull and push on
// its own.
func runGitClone(cloneURL, dest string, stdout, stderr io.Writer) error {
	cmd := exec.Command("git", "clone", "--quiet", cloneURL, dest)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func runGitCheckout(dir, ref string, stdout, stderr io.Writer) error {
	cmd := exec.Command("git", "-C", dir, "checkout", "--quiet", ref)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// describeCloneAPIError turns server error payloads into a single-line,
// user-actionable message. Keeps the switch narrow — server-side strings
// may change, so we lean on the status code.
//
// 403 and 404 both name the organization the CLI acted as: the server
// answers "app not found" for an app in another org on purpose (no existence
// leak across orgs), so from the CLI the most common cause of either is a
// pinned org that is not the app's — and the fix is dibbla org use.
func describeCloneAPIError(err *vcs.APIError, cfg *config.Config) string {
	switch err.StatusCode {
	case 401:
		return "Authentication failed. Your token may be invalid or expired. Try 'dibbla login' again."
	case 403:
		return "Access denied for " + describeOrg(cfg) + ". " + orgSwitchHint(cfg)
	case 404:
		return "App not found in " + describeOrg(cfg) + " (or version control is not enabled for this environment). " + orgSwitchHint(cfg)
	default:
		return fmt.Sprintf("Error (HTTP %d): %s", err.StatusCode, err.Body)
	}
}

// describeOrg names the organization the request went out as, the way the
// user would recognise it.
func describeOrg(cfg *config.Config) string {
	switch {
	case cfg == nil || cfg.OrgID == "":
		return "your account's default organization"
	case cfg.OrgName != "":
		return fmt.Sprintf("organization %s (%s)", cfg.OrgName, cfg.OrgID)
	default:
		return "organization " + cfg.OrgID
	}
}

func orgSwitchHint(cfg *config.Config) string {
	if cfg != nil && cfg.OrgID != "" {
		return "If the app lives in another organization: dibbla org list, then dibbla org use <name> (or --org <id>)."
	}
	return "If the app lives in another organization: dibbla org list, then dibbla org use <name>."
}
