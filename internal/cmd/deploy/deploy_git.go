package deploy

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
	"github.com/dibbla-agents/dibbla-cli/internal/gitcred"
	"github.com/dibbla-agents/dibbla-cli/internal/gitlink"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
	"github.com/spf13/cobra"
)

// `dibbla deploy` in a folder linked to its app (DIB-906).
//
// A linked folder (dibbla clone / dibbla link) shares one history with
// Dibbla: its main IS the app's main. A tarball deploy from such a folder
// would write a synthetic commit on Dibbla's main that the folder never
// sees, and the next git push would be refused as not a fast-forward — two
// histories for one app. So in a linked folder `dibbla deploy` is sugar for
// what git push already does: commit everything, push main, follow the
// deploy the push started. Old habits, skills and scripts keep working and
// the history stays one. Folders without a Dibbla remote take the tarball
// path exactly as before.

// gitDeployInput is what the git path needs from the command.
type gitDeployInput struct {
	// Dir is the repository root; Remote the remote name that points at
	// Dibbla; Branch the branch that deploys (main).
	Dir, Remote, Branch string
	App                 string
	Message             string
	// Force redeploys even when main at Dibbla already carries HEAD.
	Force            bool
	APIURL, APIToken string
}

// gitFlagsNotInGitMode are the tarball-deploy flags that have no meaning
// when the deploy is a push: the server builds the pushed commit with the
// app's stored configuration. Naming them beats ignoring them.
var gitFlagsNotInGitMode = []string{
	"env", "env-file", "cpu", "memory", "port", "favicon", "require-login",
	"access-policy", "google-scopes", "microsoft-scopes", "target-env",
	"profile", "no-public",
}

// linkedFolder reports the repository root and Dibbla remote when dir IS a
// folder linked to an app. A subdirectory of a linked repository is not:
// deploying ./api out of a repository linked to shop has always meant "the
// app called api, from this tree", and a push could only mean shop.
func linkedFolder(dir string) (top, remote string, target gitlink.Target, ok bool) {
	top = gitlink.Toplevel(dir)
	// git reports the real path (macOS /tmp is /private/tmp); compare like
	// with like.
	real, err := filepath.EvalSymlinks(dir)
	if top == "" || err != nil || top != filepath.Clean(real) {
		return "", "", gitlink.Target{}, false
	}
	remote, target, ok = gitlink.FindDibblaRemote(top)
	return top, remote, target, ok
}

// checkGitModeFlags refuses the flags a push cannot honour and an --alias
// that names another app than the folder is linked to.
func checkGitModeFlags(cmd *cobra.Command, stderr io.Writer, app string) bool {
	bad := platform.Icon("❌", "[X]")
	if deployAlias != "" && deployAlias != app {
		fmt.Fprintf(stderr, "%s this folder is linked to %s; --alias %s would deploy it as another app.\n", bad, app, deployAlias)
		fmt.Fprintln(stderr, "  hint: deploy another app from a folder that is not linked, or dibbla clone that app first.")
		return false
	}
	var used []string
	for _, name := range gitFlagsNotInGitMode {
		if cmd.Flags().Changed(name) {
			used = append(used, "--"+name)
		}
	}
	if len(used) > 0 {
		fmt.Fprintf(stderr, "%s %s cannot be set on a deploy from a linked folder: the push deploys the commit with the app's stored configuration.\n", bad, strings.Join(used, ", "))
		fmt.Fprintln(stderr, "  hint: change runtime settings with `dibbla apps update`, or put them in dibbla.yaml and commit.")
		return false
	}
	// The platform gates every deploy on REVIEW.md and the handbook
	// (DIB-966). An archive deploy carries --skip-review to the server; a
	// push carries only the commit, so the flag would skip the local check
	// and then fail on the server's — say so before pushing.
	if cmd.Flags().Changed("skip-review") {
		fmt.Fprintf(stderr, "%s --skip-review cannot be set on a deploy from a linked folder: the platform gates a push on the commit itself.\n", bad)
		fmt.Fprintln(stderr, "  hint: commit REVIEW.md and the user handbook (docs/index.md or APP.md) and deploy again.")
		return false
	}
	return true
}

// hostMismatch is set when the folder's remote points at another Dibbla
// than the CLI is logged in to (a prod clone deployed with a dev login).
// The git host is not always the API host — prod serves git.dibbla.com for
// api.dibbla.com — so a different host is only a mismatch if the server
// behind the login does not itself hand out clone URLs on that host;
// cloneHostFor asks it (nil in tests that have no server).
func hostMismatch(apiURL string, target gitlink.Target, cloneHostFor func(app string) string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return ""
	}
	if strings.EqualFold(u.Host, target.Host) {
		return ""
	}
	if cloneHostFor != nil && strings.EqualFold(cloneHostFor(target.App), target.Host) {
		return ""
	}
	return u.Host
}

// cloneHostOf answers the host the logged-in server hands out clone URLs on
// for app, or "" when it cannot say (unknown app, no VCS, network error).
func cloneHostOf(apiURL, apiToken string) func(app string) string {
	return func(app string) string {
		info, err := vcs.GetInfo(apiURL, apiToken, app)
		if err != nil || info.CloneURL == "" {
			return ""
		}
		cu, err := url.Parse(info.CloneURL)
		if err != nil {
			return ""
		}
		return cu.Host
	}
}

var operationLine = regexp.MustCompile(`(?m)operation:\s*(deployment:\S+)`)

// planRefusedPush is the pre-receive refusal of a push to main after the
// trial ended: the upgrade link on a "remote:" line.
var planRefusedPush = regexp.MustCompile(`(?m)^remote:\s+https://\S+/org-settings/plan\b`)

// pushMain is gitlink.Push, indirected so tests can add the "remote:" lines
// a real Dibbla push answers with to a push against a local bare repo.
var pushMain = gitlink.Push

// runGitDeploy commits, pushes and follows. Exit code is the deploy's own
// when a deploy ran, 0 when there was nothing new, and 1 for a refusal.
// follow is what turns the operation id the push printed into the rendered
// deploy; the command passes followDeployOperation.
func runGitDeploy(in gitDeployInput, stdout, stderr io.Writer, follow func(opID string) int) int {
	bad := platform.Icon("❌", "[X]")
	if in.Branch == "" {
		in.Branch = "main"
	}

	dirty, err := gitlink.Dirty(in.Dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s %v\n", bad, err)
		return 1
	}
	if dirty {
		if strings.TrimSpace(in.Message) == "" {
			fmt.Fprintf(stderr, "%s -m is required: this folder is linked to %s, so the changes are committed and pushed to main.\n", bad, in.App)
			fmt.Fprintln(stderr, "  example: dibbla deploy -m \"fix: handle null user\"")
			return 1
		}
		sha, err := gitlink.CommitAll(in.Dir, in.Message, false, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "%s commit: %v\n", bad, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s Committed %s: %s\n", platform.Icon("📝", "[COMMIT]"), short(sha), in.Message)
	}

	sync, err := gitlink.Compare(in.Dir, in.Remote, in.Branch, true, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s %v\n", bad, err)
		return 1
	}
	if sync.Behind > 0 {
		fmt.Fprintf(stderr, "%s fetch first: %s at Dibbla has %s this folder does not (another deploy or an MCP patch).\n", bad, sync.Upstream, plural(sync.Behind, "commit"))
		fmt.Fprintf(stderr, "  run:  git pull --rebase %s %s\n", in.Remote, in.Branch)
		fmt.Fprintln(stderr, "  then: dibbla deploy again. Nothing was deployed.")
		return 1
	}
	if sync.Ahead == 0 {
		if !in.Force {
			fmt.Fprintf(stdout, "%s Nothing new to deploy: %s at Dibbla is already at %s.\n", platform.Icon("✅", "[OK]"), sync.Upstream, short(sync.HeadSHA))
			fmt.Fprintln(stdout, "  hint: dibbla deploy --force -m \"…\" redeploys it; dibbla apps get shows which commit runs.")
			return 0
		}
		if strings.TrimSpace(in.Message) == "" {
			fmt.Fprintf(stderr, "%s -m is required with --force: the redeploy is recorded as a commit on main.\n", bad)
			return 1
		}
		sha, err := gitlink.CommitAll(in.Dir, in.Message, true, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "%s commit: %v\n", bad, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s Committed %s (redeploy, no file changes): %s\n", platform.Icon("📝", "[COMMIT]"), short(sha), in.Message)
	}

	fmt.Fprintf(stdout, "%s Pushing to %s (%s)…\n", platform.Icon("🚀", "[PUSH]"), in.App, sync.Upstream)
	out, err := pushMain(in.Dir, in.Remote, in.Branch, stderr)
	if err != nil {
		if planRefusedPush.MatchString(out) {
			// The platform's own "remote:" lines above already said it
			// (DIB-1045): the trial ended, the apps keep running, here is
			// the link. Only what the push did is added.
			fmt.Fprintf(stderr, "%s Nothing was deployed: main at Dibbla is unchanged and %s keeps running.\n", platform.Icon("●", "[i]"), in.App)
		} else if strings.Contains(out, "fetch first") || strings.Contains(out, "non-fast-forward") {
			fmt.Fprintf(stderr, "%s push rejected: main at Dibbla moved while deploying. Run git pull --rebase %s %s and deploy again.\n", bad, in.Remote, in.Branch)
		} else {
			fmt.Fprintf(stderr, "%s %v\n", bad, err)
		}
		return 1
	}
	m := operationLine.FindStringSubmatch(out)
	if m == nil {
		fmt.Fprintf(stderr, "%s main was pushed, but no deploy was started (the push output carries no operation id).\n", platform.Icon("⚠", "[!]"))
		fmt.Fprintln(stderr, "  check: dibbla apps get "+in.App)
		return 1
	}
	return follow(m[1])
}

// followDeployOperation renders the push-started operation the way
// `dibbla deploy status --follow` does, with the renderer the flags chose.
func followDeployOperation(apiURL, apiToken string) func(string) int {
	return func(opID string) int {
		var r render.Renderer
		switch {
		case deployJSON:
			r = render.NewJSON(os.Stdout)
		case deployQuiet:
			r = render.NewQuiet(os.Stdout)
		default:
			r = render.NewLog(os.Stdout, os.Stderr)
		}
		op, _, err := apps.GetOperation(apiURL, apiToken, opID)
		if err != nil {
			return reportOperationError(os.Stderr, opID, err)
		}
		return followOperation(os.Stderr, apiURL, apiToken, op, r, 2*time.Second)
	}
}

// registerGitCredentialHelper makes sure git can answer Dibbla's auth from
// the login this deploy uses, for the host the folder's remote actually
// points at; repeated registration is idempotent.
func registerGitCredentialHelper(remoteURL, apiURL string, stderr io.Writer) {
	if err := gitcred.RegisterGitHost(remoteURL, apiURL); err != nil {
		fmt.Fprintf(stderr, "%s git credential helper: %v (the push may prompt or fail)\n", platform.Icon("⚠", "[!]"), err)
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
