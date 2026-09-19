package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
)

// The five folder states from DIB-905, driven through connectFolder against a
// local bare repo standing in for the app's repo on Dibbla. The API is not
// involved: vcs.Info is built by hand with the bare repo's URL.

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func fakeDibbla(t *testing.T) *vcs.Info {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "my-app.git")
	gitT(t, root, "init", "--quiet", "-b", "main", work)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('dibbla')\n"), 0o644)
	os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	gitT(t, work, "add", ".")
	gitT(t, work, "commit", "--quiet", "-m", "deploy 1")
	gitT(t, root, "clone", "--quiet", "--bare", work, bare)
	sha := strings.TrimSpace(gitT(t, bare, "rev-parse", "HEAD"))
	return &vcs.Info{
		DefaultBranch: "main",
		LatestSHA:     sha,
		LatestCommit:  &vcs.Commit{SHA: sha, ShortSHA: sha[:7], Subject: "deploy 1"},
		CloneURL:      "file://" + bare,
	}
}

func connect(t *testing.T, dest string, explicit bool, info *vcs.Info, yes bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := connectFolder(dest, explicit, "my-app", "main", info, "", yes, &out, &out)
	return out.String(), err
}

func TestConnectEmptyFolderInPlace(t *testing.T) {
	info := fakeDibbla(t)
	dir := t.TempDir()
	out, err := connect(t, dir, true, info, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Cloned to") || !strings.Contains(out, "origin = Dibbla") {
		t.Errorf("output: %s", out)
	}
	if u := strings.TrimSpace(gitT(t, dir, "remote", "get-url", "origin")); u != info.CloneURL {
		t.Errorf("origin = %s", u)
	}
	if b := strings.TrimSpace(gitT(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); b != "main" {
		t.Errorf("branch = %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.py")); err != nil {
		t.Errorf("app.py not checked out")
	}
}

func TestConnectFilesWithoutGitGoesToSubfolder(t *testing.T) {
	info := fakeDibbla(t)
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "notes.md"), []byte("hi"), 0o644)
	// dibbla clone my-app with no --into: dest is ./my-app, not chosen by the user.
	dest := filepath.Join(cwd, "my-app")
	out, err := connect(t, dest, false, info, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Cloned to "+dest) {
		t.Errorf("should say where it cloned: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "app.py")); err != nil {
		t.Errorf("app.py missing in subfolder")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
		t.Errorf("the folder with files must not become a repo")
	}
}

func TestConnectFilesWithoutGitInPlaceLinks(t *testing.T) {
	info := fakeDibbla(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('mine')\n"), 0o644)
	out, err := connect(t, dir, true, info, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Linked") {
		t.Errorf("output: %s", out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app.py"))
	if string(b) != "print('mine')\n" {
		t.Errorf("disk must win: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Errorf("Dockerfile should arrive from Dibbla")
	}
	if !strings.Contains(gitT(t, dir, "status", "--porcelain"), " M app.py") {
		t.Errorf("git status should show app.py modified")
	}
}

func TestConnectUnbornRepoInPlace(t *testing.T) {
	info := fakeDibbla(t)
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet")
	out, err := connect(t, dir, true, info, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "already exists") || !strings.Contains(out, "Linked") {
		t.Errorf("output: %s", out)
	}
	if b := strings.TrimSpace(gitT(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); b != "main" {
		t.Errorf("branch = %s", b)
	}
	if h := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); h != info.LatestSHA {
		t.Errorf("HEAD = %s, want %s", h, info.LatestSHA)
	}
	if s := strings.TrimSpace(gitT(t, dir, "status", "--porcelain")); s != "" {
		t.Errorf("expected clean tree:\n%s", s)
	}
}

func TestConnectOwnHistoryRequiresYes(t *testing.T) {
	info := fakeDibbla(t)
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet", "-b", "main")
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('local')\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "--quiet", "-m", "local")
	local := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))

	// Without --yes (stdin is not a TTY under go test): explain, refuse, touch nothing.
	out, err := connect(t, dir, true, info, false)
	if err == nil {
		t.Fatalf("expected refusal\n%s", out)
	}
	if !strings.Contains(out, "keeps every file on disk") || !strings.Contains(out, "starts the history over") {
		t.Errorf("should explain the consequence: %s", out)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v", err)
	}
	if h := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); h != local {
		t.Errorf("refusal must not change HEAD")
	}
	if out := gitT(t, dir, "remote"); strings.TrimSpace(out) != "" {
		t.Errorf("refusal must not add a remote: %s", out)
	}

	// With --yes: linked, disk kept, status shows the diff, old commit on a branch.
	out, err = connect(t, dir, true, info, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "previous local history kept on branch pre-dibbla-") {
		t.Errorf("output: %s", out)
	}
	if h := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); h != info.LatestSHA {
		t.Errorf("HEAD = %s, want Dibbla's %s", h, info.LatestSHA)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app.py"))
	if string(b) != "print('local')\n" {
		t.Errorf("disk must win: %q", b)
	}
	st := gitT(t, dir, "status", "--porcelain")
	if !strings.Contains(st, " M app.py") {
		t.Errorf("git status should show disk vs Dibbla:\n%s", st)
	}
}

func TestConnectAlreadyLinkedPulls(t *testing.T) {
	info := fakeDibbla(t)
	root := t.TempDir()
	dir := filepath.Join(root, "a")
	gitT(t, root, "clone", "--quiet", info.CloneURL, dir)

	// A new deploy lands on Dibbla.
	other := filepath.Join(root, "b")
	gitT(t, root, "clone", "--quiet", info.CloneURL, other)
	os.WriteFile(filepath.Join(other, "app.py"), []byte("print('v2')\n"), 0o644)
	gitT(t, other, "commit", "--quiet", "-am", "deploy 2")
	gitT(t, other, "push", "--quiet", "origin", "main")

	out, err := connect(t, dir, true, info, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already linked") || !strings.Contains(out, "updated") {
		t.Errorf("output: %s", out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app.py"))
	if string(b) != "print('v2')\n" {
		t.Errorf("pull did not land: %q", b)
	}
	// Same again, no explicit dest: the folder is linked to this app, so
	// resolveCloneDest picks "." rather than ./my-app.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	cloneInto = ""
	if dest, explicit := resolveCloneDest(nil, info.CloneURL); dest != "." || !explicit {
		t.Errorf("resolveCloneDest in a linked folder = %q %v", dest, explicit)
	}
}

func TestConnectExistingSubfolderWithoutIntoRefuses(t *testing.T) {
	info := fakeDibbla(t)
	cwd := t.TempDir()
	dest := filepath.Join(cwd, "my-app")
	os.Mkdir(dest, 0o755)
	gitT(t, dest, "init", "--quiet")
	os.WriteFile(filepath.Join(dest, "x"), []byte("x"), 0o644)
	gitT(t, dest, "add", ".")
	gitT(t, dest, "commit", "--quiet", "-m", "c")
	out, err := connect(t, dest, false, info, true)
	if err == nil || !strings.Contains(err.Error(), "--into") {
		t.Fatalf("expected a hint to use --into, got %v\n%s", err, out)
	}
}

func TestConnectRefOnlyForFreshClone(t *testing.T) {
	info := fakeDibbla(t)
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet")
	var out bytes.Buffer
	err := connectFolder(dir, true, "my-app", "main", info, "abc1234", true, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "--ref only applies") {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkIsAnAliasOfClone(t *testing.T) {
	c, _, err := rootCmd.Find([]string{"link", "my-app"})
	if err != nil || c != cloneCmd {
		t.Fatalf("dibbla link should resolve to clone: %v %v", c, err)
	}
	if !c.HasAlias("link") {
		t.Error("clone must list link as an alias")
	}
}

func TestDescribeCloneAPIErrorNamesTheOrg(t *testing.T) {
	pinned := &config.Config{OrgID: "org_123", OrgName: "Acme"}
	msg := describeCloneAPIError(&vcs.APIError{StatusCode: 403}, pinned)
	if !strings.Contains(msg, "Acme") || !strings.Contains(msg, "org_123") || !strings.Contains(msg, "dibbla org use") {
		t.Errorf("403 = %q", msg)
	}
	msg = describeCloneAPIError(&vcs.APIError{StatusCode: 404}, pinned)
	if !strings.Contains(msg, "Acme") || !strings.Contains(msg, "dibbla org use") {
		t.Errorf("404 = %q", msg)
	}
	msg = describeCloneAPIError(&vcs.APIError{StatusCode: 403}, &config.Config{})
	if !strings.Contains(msg, "default organization") {
		t.Errorf("403 unpinned = %q", msg)
	}
	if msg == "Access denied." {
		t.Error("the bare Access denied is what DIB-905 retires")
	}
}

func TestBuildFolderReport(t *testing.T) {
	info := fakeDibbla(t)
	root := t.TempDir()
	dir := filepath.Join(root, "a")
	gitT(t, root, "clone", "--quiet", info.CloneURL, dir)
	// The remote must look like a Dibbla clone URL for status to recognise
	// it; the fetch itself is skipped (offline) so the URL is never dialled.
	gitT(t, dir, "remote", "set-url", "origin", "https://api.invalid/git/acme/my-app.git")

	if r := buildFolderReport(t.TempDir(), "", "", true); r != nil {
		t.Errorf("a folder outside any repo has no report: %+v", r)
	}

	r := buildFolderReport(dir, "", "", true)
	if r == nil {
		t.Fatal("expected a report")
	}
	if r.App != "my-app" || r.Org != "acme" || r.Remote != "origin" || r.Branch != "main" {
		t.Errorf("report = %+v", r)
	}
	if r.Ahead != 0 || r.Behind != 0 || r.Fetched || r.HeadSHA != info.LatestSHA {
		t.Errorf("report = %+v", r)
	}

	// One local commit, and the API says the running commit is the old one.
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('local')\n"), 0o644)
	gitT(t, dir, "commit", "--quiet", "-am", "local")
	head := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	old := lookupRunningSHA
	defer func() { lookupRunningSHA = old }()
	lookupRunningSHA = func(apiURL, token, alias string) string { return info.LatestSHA }

	// Not offline, but the remote is unreachable: the fetch fails and the
	// report falls back to the last fetch instead of erroring out.
	r = buildFolderReport(filepath.Join(dir), "https://api.invalid", "tok", false)
	if r.Ahead != 1 || r.Behind != 0 || r.Fetched {
		t.Errorf("report = %+v", r)
	}
	if r.RunningSHA != info.LatestSHA || r.RunningIsHead || r.HeadSHA != head {
		t.Errorf("running = %+v", r)
	}

	lookupRunningSHA = func(apiURL, token, alias string) string { return head }
	r = buildFolderReport(dir, "https://api.invalid", "tok", false)
	if !r.RunningIsHead {
		t.Errorf("running should be HEAD: %+v", r)
	}
}
