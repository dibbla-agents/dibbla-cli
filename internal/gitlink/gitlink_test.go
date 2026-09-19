package gitlink

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// run executes git in dir and fails the test on error.
func run(t *testing.T, dir string, args ...string) string {
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
	return string(out)
}

// dibblaRepo builds a bare repo standing in for the app's repo on Dibbla:
// main with two commits, files app.py and README.md. Returns its clone URL.
func dibblaRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "app.git")
	run(t, root, "init", "--quiet", "-b", "main", work)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v1')\n"), 0o644)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("# app\n"), 0o644)
	run(t, work, "add", ".")
	run(t, work, "commit", "--quiet", "-m", "deploy 1")
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	run(t, work, "commit", "--quiet", "-am", "deploy 2")
	run(t, root, "clone", "--quiet", "--bare", work, bare)
	return "file://" + bare
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestInspectModes(t *testing.T) {
	url := dibblaRepo(t)
	root := t.TempDir()

	missing := filepath.Join(root, "nope")
	empty := filepath.Join(root, "empty")
	os.Mkdir(empty, 0o755)
	files := filepath.Join(root, "files")
	os.Mkdir(files, 0o755)
	write(t, files, "notes.txt", "x")
	unborn := filepath.Join(root, "unborn")
	run(t, root, "init", "--quiet", unborn)
	history := filepath.Join(root, "history")
	run(t, root, "init", "--quiet", "-b", "main", history)
	write(t, history, "a.txt", "a")
	run(t, history, "add", ".")
	run(t, history, "commit", "--quiet", "-m", "local")
	linked := filepath.Join(root, "linked")
	run(t, root, "clone", "--quiet", url, linked)
	other := filepath.Join(root, "other")
	run(t, root, "init", "--quiet", other)
	run(t, other, "remote", "add", "origin", "https://github.com/acme/app.git")

	cases := []struct {
		dir  string
		mode Mode
	}{
		{missing, ModeMissing},
		{empty, ModeEmpty},
		{files, ModeFiles},
		{unborn, ModeRepoUnborn},
		{history, ModeRepoHistory},
		{linked, ModeLinked},
		{other, ModeRepoUnborn},
	}
	for _, c := range cases {
		st, err := Inspect(c.dir, url)
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}
		if st.Mode != c.mode {
			t.Errorf("%s: mode %s, want %s", filepath.Base(c.dir), st.Mode, c.mode)
		}
	}
	st, _ := Inspect(linked, url)
	if st.Remote != "origin" || st.Commits != 2 || st.Branch != "main" {
		t.Errorf("linked state = %+v", st)
	}
	st, _ = Inspect(other, url)
	if !st.OriginTaken {
		t.Errorf("origin pointing at GitHub should be reported taken: %+v", st)
	}
	st, _ = Inspect(history, url)
	if st.Commits != 1 || st.OriginTaken {
		t.Errorf("history state = %+v", st)
	}
}

func TestLinkUnbornRepoChecksOutMain(t *testing.T) {
	url := dibblaRepo(t)
	dir := filepath.Join(t.TempDir(), "d")
	run(t, filepath.Dir(dir), "init", "--quiet", dir)

	res, err := Link(dir, url, "main", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "origin" || res.Backup != "" || res.Restored != 2 {
		t.Errorf("result = %+v", res)
	}
	if got := read(t, dir, "app.py"); got != "print('v2')\n" {
		t.Errorf("app.py = %q", got)
	}
	if out := run(t, dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("expected clean tree, got:\n%s", out)
	}
	if b := strings.TrimSpace(run(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); b != "main" {
		t.Errorf("branch = %s", b)
	}
	if u := strings.TrimSpace(run(t, dir, "rev-parse", "--abbrev-ref", "main@{upstream}")); u != "origin/main" {
		t.Errorf("upstream = %s", u)
	}
	st, _ := Inspect(dir, url)
	if st.Mode != ModeLinked {
		t.Errorf("after link mode = %s", st.Mode)
	}
}

func TestLinkOwnHistoryKeepsDiskAndShowsDiff(t *testing.T) {
	url := dibblaRepo(t)
	dir := filepath.Join(t.TempDir(), "d")
	run(t, filepath.Dir(dir), "init", "--quiet", "-b", "master", dir)
	write(t, dir, "app.py", "print('local')\n")
	write(t, dir, "extra.txt", "mine\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "--quiet", "-m", "local work")
	localHead := strings.TrimSpace(run(t, dir, "rev-parse", "HEAD"))

	res, err := Link(dir, url, "main", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Backup, "pre-dibbla-") {
		t.Errorf("expected a backup branch, got %+v", res)
	}
	if res.Restored != 1 { // README.md
		t.Errorf("restored = %d, want 1", res.Restored)
	}
	// Disk wins: the local app.py is untouched, README.md arrived from Dibbla.
	if got := read(t, dir, "app.py"); got != "print('local')\n" {
		t.Errorf("app.py overwritten: %q", got)
	}
	if got := read(t, dir, "README.md"); got != "# app\n" {
		t.Errorf("README.md = %q", got)
	}
	// History starts over from Dibbla's main; the old commit is kept.
	if h := strings.TrimSpace(run(t, dir, "rev-parse", "HEAD")); h == localHead {
		t.Errorf("HEAD still local")
	}
	if b := strings.TrimSpace(run(t, dir, "rev-parse", res.Backup)); b != localHead {
		t.Errorf("backup %s = %s, want %s", res.Backup, b, localHead)
	}
	status := run(t, dir, "status", "--porcelain")
	if !strings.Contains(status, " M app.py") || !strings.Contains(status, "?? extra.txt") {
		t.Errorf("git status should show disk vs Dibbla:\n%s", status)
	}
	if n := strings.TrimSpace(run(t, dir, "rev-list", "--count", "HEAD")); n != "2" {
		t.Errorf("HEAD should have Dibbla's 2 commits, got %s", n)
	}
}

func TestLinkFilesWithoutGit(t *testing.T) {
	url := dibblaRepo(t)
	dir := filepath.Join(t.TempDir(), "d")
	os.Mkdir(dir, 0o755)
	write(t, dir, "README.md", "my readme\n")

	res, err := Link(dir, url, "main", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 || res.Backup != "" {
		t.Errorf("result = %+v", res)
	}
	if got := read(t, dir, "README.md"); got != "my readme\n" {
		t.Errorf("README.md overwritten: %q", got)
	}
	if got := read(t, dir, "app.py"); got != "print('v2')\n" {
		t.Errorf("app.py = %q", got)
	}
}

func TestLinkUsesDibblaRemoteWhenOriginTaken(t *testing.T) {
	url := dibblaRepo(t)
	dir := filepath.Join(t.TempDir(), "d")
	run(t, filepath.Dir(dir), "init", "--quiet", dir)
	run(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")

	res, err := Link(dir, url, "main", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "dibbla" {
		t.Errorf("remote = %s", res.Remote)
	}
	st, _ := Inspect(dir, url)
	if st.Mode != ModeLinked || st.Remote != "dibbla" {
		t.Errorf("state = %+v", st)
	}
}

func TestLinkRefusesLinkedRepo(t *testing.T) {
	url := dibblaRepo(t)
	dir := filepath.Join(t.TempDir(), "d")
	run(t, filepath.Dir(dir), "clone", "--quiet", url, dir)
	if _, err := Link(dir, url, "main", io.Discard); err == nil {
		t.Fatal("expected error")
	}
}

func TestPullAndCompare(t *testing.T) {
	url := dibblaRepo(t)
	bare := strings.TrimPrefix(url, "file://")
	root := t.TempDir()
	a := filepath.Join(root, "a")
	run(t, root, "clone", "--quiet", url, a)

	s, err := Compare(a, "origin", "main", true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ahead != 0 || s.Behind != 0 || s.Dirty {
		t.Errorf("fresh clone sync = %+v", s)
	}

	// A deploy lands on Dibbla: a is now behind by one.
	b := filepath.Join(root, "b")
	run(t, root, "clone", "--quiet", url, b)
	write(t, b, "app.py", "print('v3')\n")
	run(t, b, "commit", "--quiet", "-am", "deploy 3")
	run(t, b, "push", "--quiet", "origin", "main")
	_ = bare

	s, _ = Compare(a, "origin", "main", true, io.Discard)
	if s.Behind != 1 || s.Ahead != 0 {
		t.Errorf("after remote deploy sync = %+v", s)
	}

	// Local commit: ahead by one too.
	write(t, a, "local.txt", "x")
	run(t, a, "add", ".")
	run(t, a, "commit", "--quiet", "-m", "local")
	s, _ = Compare(a, "origin", "main", false, io.Discard)
	if s.Behind != 1 || s.Ahead != 1 {
		t.Errorf("diverged sync = %+v", s)
	}
	if err := Pull(a, "origin", io.Discard); err == nil {
		t.Error("ff-only pull should fail when diverged")
	}
	run(t, a, "reset", "--quiet", "--hard", "HEAD~1")
	if err := Pull(a, "origin", io.Discard); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := read(t, a, "app.py"); got != "print('v3')\n" {
		t.Errorf("after pull app.py = %q", got)
	}
}

func TestParseCloneURL(t *testing.T) {
	tgt, ok := ParseCloneURL("https://api.dibbla.com/git/acme/my-app.git")
	if !ok || tgt.Org != "acme" || tgt.App != "my-app" || tgt.Host != "api.dibbla.com" {
		t.Errorf("got %+v %v", tgt, ok)
	}
	if _, ok := ParseCloneURL("https://github.com/acme/my-app.git"); ok {
		t.Error("GitHub URL should not parse as Dibbla")
	}
	if _, ok := ParseCloneURL("git@github.com:acme/my-app.git"); ok {
		t.Error("scp URL should not parse as Dibbla")
	}
	if !SameRepo("https://API.dibbla.com/git/acme/my-app.git", "https://api.dibbla.com/git/acme/my-app") {
		t.Error("SameRepo should ignore case and .git")
	}
	if SameRepo("https://api.dibbla.com/git/acme/my-app.git", "https://api.dibbla.com/git/acme/other.git") {
		t.Error("different apps are not the same repo")
	}
}

func TestFindDibblaRemote(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "--quiet")
	if _, _, ok := FindDibblaRemote(dir); ok {
		t.Error("no remotes → not found")
	}
	run(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	run(t, dir, "remote", "add", "dibbla", "https://api.dibbla.com/git/acme/app.git")
	name, tgt, ok := FindDibblaRemote(dir)
	if !ok || name != "dibbla" || tgt.App != "app" || tgt.Org != "acme" {
		t.Errorf("got %s %+v %v", name, tgt, ok)
	}
	if Toplevel(dir) != filepath.Clean(mustEval(t, dir)) {
		t.Errorf("toplevel = %s", Toplevel(dir))
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
