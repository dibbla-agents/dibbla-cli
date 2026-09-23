package deploy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/gitlink"
)

// The Dibbla side in these tests is a local bare repository reached through
// a Dibbla-shaped https URL: `url.<bare>.insteadOf <https>` makes git talk
// to the bare repo while the remote's configured URL still parses as a
// Dibbla clone URL, which is what makes the folder "linked".
const testCloneURL = "https://api.test.invalid/api/deploy/git/acme/shop.git"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// linkedRepo returns a working clone linked to a bare "Dibbla" repo, plus
// the bare path. main has one commit.
func linkedRepo(t *testing.T) (work, bare string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	bare = filepath.Join(root, "shop.git")
	git(t, root, "init", "--quiet", "-b", "main", seed)
	os.WriteFile(filepath.Join(seed, "app.py"), []byte("print('v1')\n"), 0o644)
	git(t, seed, "add", ".")
	git(t, seed, "commit", "--quiet", "-m", "deploy 1")
	git(t, root, "clone", "--quiet", "--bare", seed, bare)

	work = filepath.Join(root, "work")
	git(t, root, "clone", "--quiet", bare, work)
	git(t, work, "config", "url."+bare+".insteadOf", testCloneURL)
	git(t, work, "remote", "set-url", "origin", testCloneURL)
	git(t, work, "config", "user.name", "t")
	git(t, work, "config", "user.email", "t@example.com")
	return work, bare
}

func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func input(work string, msg string, force bool) gitDeployInput {
	return gitDeployInput{Dir: work, Remote: "origin", Branch: "main", App: "shop", Message: msg, Force: force}
}

func TestLinkedFolderIsTheRepositoryRootOnly(t *testing.T) {
	gitEnv(t)
	work, _ := linkedRepo(t)
	top, remote, target, ok := linkedFolder(work)
	if !ok || remote != "origin" || target.App != "shop" || target.Org != "acme" {
		t.Fatalf("root not recognised as linked: top=%q remote=%q target=%+v ok=%v", top, remote, target, ok)
	}
	sub := filepath.Join(work, "api")
	os.MkdirAll(sub, 0o755)
	if _, _, _, ok := linkedFolder(sub); ok {
		t.Fatal("a subdirectory of a linked repo must take the tarball path")
	}
	if _, _, _, ok := linkedFolder(t.TempDir()); ok {
		t.Fatal("a folder without git must take the tarball path")
	}
	plain := t.TempDir()
	git(t, plain, "init", "--quiet")
	git(t, plain, "remote", "add", "origin", "https://github.com/acme/shop.git")
	if _, _, _, ok := linkedFolder(plain); ok {
		t.Fatal("a repo with only a GitHub remote must take the tarball path")
	}
}

func TestGitDeployCommitsEverythingPushesAndFollows(t *testing.T) {
	gitEnv(t)
	work, bare := linkedRepo(t)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	os.WriteFile(filepath.Join(work, "new.txt"), []byte("untracked\n"), 0o644)
	os.WriteFile(filepath.Join(work, ".gitignore"), []byte("secret.env\n"), 0o644)
	os.WriteFile(filepath.Join(work, "secret.env"), []byte("KEY=1\n"), 0o644)

	// A local bare repo prints no operation id; stand in for the hook.
	pushed := stubPushOutput(t, "remote: Dibbla: deploying 4f2a9c1e0b7d to shop\nremote:   operation: deployment:op-1\nremote:   follow:    dibbla deploy status deployment:op-1 --follow\n")

	var stdout, stderr bytes.Buffer
	var followed string
	code := runGitDeploy(input(work, "feat: v2", false), &stdout, &stderr, func(id string) int { followed = id; return 0 })
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if followed != "deployment:op-1" {
		t.Fatalf("followed %q, want the operation the push printed", followed)
	}
	if !*pushed {
		t.Fatal("push never ran")
	}
	head := git(t, work, "rev-parse", "HEAD")
	if got := git(t, bare, "rev-parse", "refs/heads/main"); got != head {
		t.Fatalf("Dibbla main %s, local HEAD %s: push did not land", got, head)
	}
	if subj := git(t, work, "log", "-1", "--format=%s"); subj != "feat: v2" {
		t.Fatalf("commit subject %q", subj)
	}
	files := git(t, work, "ls-tree", "--name-only", "HEAD")
	for _, want := range []string{"app.py", "new.txt", ".gitignore"} {
		if !strings.Contains(files, want) {
			t.Errorf("commit lacks %s: %s", want, files)
		}
	}
	if strings.Contains(files, "secret.env") {
		t.Errorf(".gitignore'd file was committed: %s", files)
	}
	if !strings.Contains(stdout.String(), "Committed "+head[:12]) {
		t.Errorf("stdout lacks the commit line:\n%s", stdout.String())
	}
}

func TestGitDeployRequiresMessageForUncommittedChanges(t *testing.T) {
	gitEnv(t)
	work, bare := linkedRepo(t)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	before := git(t, bare, "rev-parse", "refs/heads/main")

	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "", false), &stdout, &stderr, func(string) int { t.Fatal("must not deploy"); return 0 })
	if code != 1 || !strings.Contains(stderr.String(), "-m is required") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
	if git(t, bare, "rev-parse", "refs/heads/main") != before {
		t.Fatal("Dibbla main moved")
	}
	if git(t, work, "status", "--porcelain") == "" {
		t.Fatal("changes were committed without a message")
	}
}

func TestGitDeployNothingNewUnlessForced(t *testing.T) {
	gitEnv(t)
	work, bare := linkedRepo(t)
	before := git(t, bare, "rev-parse", "refs/heads/main")

	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "", false), &stdout, &stderr, func(string) int { t.Fatal("must not deploy"); return 0 })
	if code != 0 || !strings.Contains(stdout.String(), "Nothing new to deploy") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if git(t, bare, "rev-parse", "refs/heads/main") != before {
		t.Fatal("Dibbla main moved")
	}

	// --force without -m: refused, still nothing pushed.
	stdout.Reset()
	stderr.Reset()
	code = runGitDeploy(input(work, "", true), &stdout, &stderr, func(string) int { t.Fatal("must not deploy"); return 0 })
	if code != 1 || !strings.Contains(stderr.String(), "-m is required with --force") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}

	// --force -m: an empty commit on main, pushed and followed.
	stubPushOutput(t, "remote:   operation: deployment:op-2\n")
	stdout.Reset()
	stderr.Reset()
	var followed string
	code = runGitDeploy(input(work, "redeploy: retry", true), &stdout, &stderr, func(id string) int { followed = id; return 0 })
	if code != 0 || followed != "deployment:op-2" {
		t.Fatalf("exit %d followed %q\nstdout:\n%s\nstderr:\n%s", code, followed, stdout.String(), stderr.String())
	}
	head := git(t, work, "rev-parse", "HEAD")
	if head == before || git(t, bare, "rev-parse", "refs/heads/main") != head {
		t.Fatalf("forced redeploy did not push a new commit (before %s, head %s)", before, head)
	}
	if tree := git(t, work, "rev-parse", "HEAD^{tree}"); tree != git(t, work, "rev-parse", before+"^{tree}") {
		t.Fatal("forced redeploy changed the tree")
	}
}

func TestGitDeployRefusesWhenDibblaMainIsAhead(t *testing.T) {
	gitEnv(t)
	work, bare := linkedRepo(t)
	// An MCP patch lands on Dibbla's main behind the folder's back.
	other := filepath.Join(t.TempDir(), "other")
	git(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	git(t, other, "config", "user.name", "t")
	git(t, other, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(other, "patched.txt"), []byte("mcp\n"), 0o644)
	git(t, other, "add", ".")
	git(t, other, "commit", "--quiet", "-m", "mcp patch")
	git(t, other, "push", "--quiet", "origin", "main")
	remoteHead := git(t, bare, "rev-parse", "refs/heads/main")

	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "feat: v2", false), &stdout, &stderr, func(string) int { t.Fatal("must not deploy"); return 0 })
	if code != 1 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"fetch first", "git pull --rebase origin main", "Nothing was deployed"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	if git(t, bare, "rev-parse", "refs/heads/main") != remoteHead {
		t.Fatal("Dibbla main moved")
	}
	// The local change is safe in a commit, ready for the rebase.
	if git(t, work, "status", "--porcelain") != "" || git(t, work, "log", "-1", "--format=%s") != "feat: v2" {
		t.Fatal("local change was not committed before the refusal")
	}
}

func TestGitDeployWithoutOperationIdFails(t *testing.T) {
	gitEnv(t)
	work, _ := linkedRepo(t)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "feat: v2", false), &stdout, &stderr, func(string) int { t.Fatal("nothing to follow"); return 0 })
	if code != 1 || !strings.Contains(stderr.String(), "no deploy was started") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
}

// stubPushOutput makes the push helper append hookOutput to what git
// printed, the way receive-pack relays the post-receive hook's lines.
func stubPushOutput(t *testing.T, hookOutput string) *bool {
	t.Helper()
	ran := false
	orig := pushMain
	pushMain = func(dir, remote, branch string, stderr io.Writer) (string, error) {
		ran = true
		out, err := orig(dir, remote, branch, stderr)
		if err != nil {
			return out, err
		}
		io.WriteString(stderr, hookOutput)
		return out + hookOutput, nil
	}
	t.Cleanup(func() { pushMain = orig })
	return &ran
}

// Prod hands out clone URLs on git.dibbla.com while the login is
// api.dibbla.com; that is not a mismatch when the server itself says so.
func TestHostMismatch_GitHostDifferentFromAPIHostIsFineWhenTheServerSaysSo(t *testing.T) {
	target := gitlink.Target{Host: "git.dibbla.com", Org: "acme", App: "shop"}
	server := func(app string) string {
		if app == "shop" {
			return "git.dibbla.com"
		}
		return ""
	}
	if got := hostMismatch("https://api.dibbla.com", target, server); got != "" {
		t.Errorf("server-issued git host flagged as mismatch: %q", got)
	}
	if got := hostMismatch("https://api.dibbla.net", target, func(string) string { return "api.dibbla.net" }); got != "api.dibbla.net" {
		t.Errorf("a prod clone under a dev login must still be refused, got %q", got)
	}
	if got := hostMismatch("https://api.dibbla.com", target, nil); got != "api.dibbla.com" {
		t.Errorf("with nothing to ask, a different host is a mismatch, got %q", got)
	}
}

// After the trial ended the platform refuses main in pre-receive and says why
// on its own "remote:" lines (DIB-1045). The CLI adds only what the push did,
// never a bare "git push: exit status 1".
func TestGitDeployAfterTheTrialEndedSaysNothingWasDeployed(t *testing.T) {
	gitEnv(t)
	work, _ := linkedRepo(t)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	refusal := "remote: Your free trial has ended — thanks for trying Dibbla!\n" +
		"remote: Your apps keep running exactly as they are. To deploy again, upgrade to Business:\n" +
		"remote:   https://console.dibbla.com/org-settings/plan?upgrade=review\n" +
		" ! [remote rejected] HEAD -> main (pre-receive hook declined)\n"
	orig := pushMain
	pushMain = func(dir, remote, branch string, stderr io.Writer) (string, error) {
		io.WriteString(stderr, refusal)
		return refusal, errors.New("git push: exit status 1")
	}
	t.Cleanup(func() { pushMain = orig })

	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "feat: v2", false), &stdout, &stderr, func(string) int { t.Fatal("nothing to follow"); return 0 })
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	got := stderr.String()
	for _, want := range []string{"https://console.dibbla.com/org-settings/plan?upgrade=review", "Nothing was deployed", "keeps running"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "exit status") {
		t.Errorf("a bare git exit reached the customer:\n%s", got)
	}
}
