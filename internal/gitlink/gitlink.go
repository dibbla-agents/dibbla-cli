// Package gitlink connects a local directory to an app's Dibbla git repository.
//
// `git clone` handles exactly one starting point: a directory that does not
// exist or is empty. Coding agents rarely hand us that one — Codex refuses to
// work outside a repository, so the folder is `git init`ed and empty; Claude
// commits out of habit, so it has its own history; a human has files but no
// .git at all. This package looks at the directory first and then does the
// git work that fits, so nobody has to run git clone by hand and read
// "destination path '.' already exists and is not an empty directory".
//
// The rule for a directory that already has content is: disk wins, history
// starts over from Dibbla's main. Files on disk are never overwritten; files
// Dibbla has that the disk lacks are checked out; whatever differs is left as
// uncommitted changes for `git status` to show. Merging two histories is out
// of scope by decision.
package gitlink

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Mode is what the directory looked like before we touched it.
type Mode int

const (
	// ModeMissing: the directory does not exist. git clone creates it.
	ModeMissing Mode = iota
	// ModeEmpty: exists, no entries. git clone accepts it.
	ModeEmpty
	// ModeFiles: has files but no .git of its own.
	ModeFiles
	// ModeRepoUnborn: a repository with no commits (fresh `git init`).
	ModeRepoUnborn
	// ModeRepoHistory: a repository with commits and no Dibbla remote.
	ModeRepoHistory
	// ModeLinked: a repository with a remote pointing at this app on Dibbla.
	ModeLinked
)

func (m Mode) String() string {
	switch m {
	case ModeMissing:
		return "missing"
	case ModeEmpty:
		return "empty"
	case ModeFiles:
		return "files"
	case ModeRepoUnborn:
		return "repo-unborn"
	case ModeRepoHistory:
		return "repo-history"
	case ModeLinked:
		return "linked"
	}
	return "unknown"
}

// State is the result of Inspect.
type State struct {
	Mode Mode
	// Remote is the name of the remote that points at Dibbla when Mode is
	// ModeLinked ("origin" in every clone this CLI made).
	Remote string
	// HasFiles reports whether the working tree holds anything besides .git.
	HasFiles bool
	// Commits is the number of commits reachable from HEAD (0 when unborn).
	Commits int
	// Branch is the current branch name, "" when detached or unborn.
	Branch string
	// OriginTaken is true when a remote named origin exists and does not
	// point at Dibbla, so the link has to use another name.
	OriginTaken bool
}

// Inspect classifies dir against cloneURL. It never changes anything.
func Inspect(dir, cloneURL string) (State, error) {
	var st State
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		st.Mode = ModeMissing
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if !fi.IsDir() {
		return st, fmt.Errorf("%s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return st, err
	}
	hasGit := false
	for _, e := range entries {
		if e.Name() == ".git" {
			hasGit = true
			continue
		}
		st.HasFiles = true
	}
	if !hasGit {
		if st.HasFiles {
			st.Mode = ModeFiles
		} else {
			st.Mode = ModeEmpty
		}
		return st, nil
	}

	// A .git entry that git itself does not accept (a stray file, a broken
	// worktree pointer) is not a repository we can link; say so rather than
	// guessing.
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return st, fmt.Errorf("%s has a .git entry git does not recognise as a repository", dir)
	}

	remotes, err := gitRemotes(dir)
	if err != nil {
		return st, err
	}
	for name, u := range remotes {
		if SameRepo(u, cloneURL) {
			st.Remote = name
		} else if name == "origin" {
			st.OriginTaken = true
		}
	}
	if st.Remote == "origin" {
		st.OriginTaken = false
	}

	if out, err := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		st.Branch = strings.TrimSpace(out)
	}
	if out, err := git(dir, "rev-list", "--count", "HEAD"); err == nil {
		st.Commits, _ = strconv.Atoi(strings.TrimSpace(out))
	}

	switch {
	case st.Remote != "":
		st.Mode = ModeLinked
	case st.Commits == 0:
		st.Mode = ModeRepoUnborn
	default:
		st.Mode = ModeRepoHistory
	}
	return st, nil
}

// SameRepo reports whether two git URLs name the same Dibbla repository:
// same host and path, ignoring scheme case, credentials in the URL, a
// trailing slash and the optional .git suffix.
func SameRepo(a, b string) bool {
	return normalizeRepoURL(a) != "" && normalizeRepoURL(a) == normalizeRepoURL(b)
}

func normalizeRepoURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Host == "" && u.Path == "") {
		return ""
	}
	// Local paths and file:// URLs (tests, and nothing else) have no host;
	// the path alone identifies them.
	p := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	return strings.ToLower(u.Host) + p
}

// Target is the org and app a Dibbla clone URL points at, parsed from its
// path: <base>/git/<org>/<app>.git.
type Target struct {
	Host string
	Org  string
	App  string
}

// ParseCloneURL extracts the org slug and app alias from a Dibbla clone URL.
// ok is false for any URL that is not shaped like one, including other
// hosts' remotes, so callers can tell a Dibbla remote from a GitHub one.
func ParseCloneURL(raw string) (Target, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return Target{}, false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	// The /git/ segment may sit under a prefix on unusual hosts; locate it
	// rather than assuming it comes first.
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] == "git" && i+2 == len(segs)-1 {
			app := strings.TrimSuffix(segs[i+2], ".git")
			if segs[i+1] == "" || app == "" {
				return Target{}, false
			}
			return Target{Host: strings.ToLower(u.Host), Org: segs[i+1], App: app}, true
		}
	}
	return Target{}, false
}

// FindDibblaRemote looks through dir's remotes for one whose URL parses as a
// Dibbla clone URL, without needing to know the app in advance. This is what
// `dibbla status` uses to describe the folder it was run in.
func FindDibblaRemote(dir string) (name string, t Target, ok bool) {
	remotes, err := gitRemotes(dir)
	if err != nil {
		return "", Target{}, false
	}
	// Prefer origin when several remotes qualify.
	if u, has := remotes["origin"]; has {
		if t, ok := ParseCloneURL(u); ok {
			return "origin", t, true
		}
	}
	for n, u := range remotes {
		if t, ok := ParseCloneURL(u); ok {
			return n, t, true
		}
	}
	return "", Target{}, false
}

// Result describes what Link did, for the caller to narrate.
type Result struct {
	Remote string
	// Backup is the branch that keeps the pre-link history, "" when there
	// was none to keep.
	Backup string
	// Restored is the number of files checked out from Dibbla because the
	// disk did not have them.
	Restored int
}

// Link attaches dir — an existing repository, born or not — to cloneURL and
// makes main track Dibbla's main without touching a single file already on
// disk. Steps, in order:
//
//  1. remote add (origin, or "dibbla" when origin already points elsewhere);
//  2. fetch;
//  3. if HEAD has commits, keep them reachable on a backup branch;
//  4. point HEAD at main and reset the index to <remote>/main — a mixed
//     reset, so the working tree is left alone;
//  5. check out only the files Dibbla has and the disk lacks;
//  6. set main's upstream.
//
// Afterwards `git status` shows exactly the difference between the disk and
// Dibbla, as changes to commit.
func Link(dir, cloneURL, branch string, stderr io.Writer) (Result, error) {
	var res Result
	if branch == "" {
		branch = "main"
	}
	st, err := Inspect(dir, cloneURL)
	if err != nil {
		return res, err
	}
	switch st.Mode {
	case ModeLinked:
		return res, fmt.Errorf("already linked via remote %q", st.Remote)
	case ModeMissing, ModeEmpty, ModeFiles:
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return res, err
		}
		if _, err := gitOut(dir, stderr, "init", "--quiet"); err != nil {
			return res, err
		}
	}

	res.Remote = "origin"
	if st.OriginTaken {
		res.Remote = "dibbla"
	}
	if _, err := gitOut(dir, stderr, "remote", "add", res.Remote, cloneURL); err != nil {
		return res, err
	}
	if _, err := gitOut(dir, stderr, "fetch", "--quiet", res.Remote); err != nil {
		return res, fmt.Errorf("fetch from Dibbla: %w", err)
	}
	upstream := res.Remote + "/" + branch
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", upstream+"^{commit}"); err != nil {
		return res, fmt.Errorf("Dibbla has no %s branch for this app yet", branch)
	}

	if st.Commits > 0 {
		res.Backup = "pre-dibbla-" + time.Now().Format("20060102-150405")
		if _, err := gitOut(dir, stderr, "branch", res.Backup, "HEAD"); err != nil {
			return res, err
		}
	}

	if _, err := gitOut(dir, stderr, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return res, err
	}
	if _, err := gitOut(dir, stderr, "reset", "--quiet", "--mixed", upstream); err != nil {
		return res, err
	}
	n, err := restoreMissing(dir, stderr)
	if err != nil {
		return res, err
	}
	res.Restored = n
	if _, err := gitOut(dir, stderr, "branch", "--set-upstream-to="+upstream, branch); err != nil {
		return res, err
	}
	return res, nil
}

// restoreMissing checks out every path the index has and the working tree
// does not. Files that exist on disk are never touched, whatever they hold.
func restoreMissing(dir string, stderr io.Writer) (int, error) {
	out, err := git(dir, "ls-files", "-z", "--deleted")
	if err != nil {
		return 0, err
	}
	paths := bytes.Split(bytes.TrimSuffix([]byte(out), []byte{0}), []byte{0})
	if len(out) == 0 {
		return 0, nil
	}
	cmd := exec.Command("git", "-C", dir, "checkout", "--quiet", "--pathspec-from-file=-", "--pathspec-file-nul", "--")
	cmd.Stdin = bytes.NewReader([]byte(out))
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("check out files from Dibbla: %w", err)
	}
	return len(paths), nil
}

// Pull fast-forwards the current branch from the Dibbla remote. It fails —
// leaving everything as it was — when local and remote have diverged.
func Pull(dir, remote string, stderr io.Writer) error {
	_, err := gitOut(dir, stderr, "pull", "--quiet", "--ff-only", remote)
	return err
}

// Sync is how the folder relates to Dibbla, for `dibbla status`.
type Sync struct {
	HeadSHA string
	Ahead   int
	Behind  int
	// Upstream is <remote>/<branch> the counts were taken against.
	Upstream string
	// Dirty is true when the working tree or index has uncommitted changes.
	Dirty bool
}

// Compare counts commits between HEAD and <remote>/<branch>. Fetch first when
// the answer should reflect Dibbla right now; without it the counts describe
// the last fetch.
func Compare(dir, remote, branch string, fetch bool, stderr io.Writer) (Sync, error) {
	var s Sync
	if branch == "" {
		branch = "main"
	}
	if fetch {
		if _, err := gitOut(dir, stderr, "fetch", "--quiet", remote); err != nil {
			return s, fmt.Errorf("fetch from Dibbla: %w", err)
		}
	}
	s.Upstream = remote + "/" + branch
	if out, err := git(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		s.HeadSHA = strings.TrimSpace(out)
	}
	out, err := git(dir, "rev-list", "--left-right", "--count", "HEAD..."+s.Upstream)
	if err != nil {
		return s, fmt.Errorf("compare with %s: %w", s.Upstream, err)
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		s.Ahead, _ = strconv.Atoi(f[0])
		s.Behind, _ = strconv.Atoi(f[1])
	}
	if out, err := git(dir, "status", "--porcelain"); err == nil {
		s.Dirty = strings.TrimSpace(out) != ""
	}
	return s, nil
}

// Toplevel returns the root of the repository containing dir, or "" when dir
// is not inside one. Used so `dibbla status` in a subdirectory of a linked
// folder still describes it.
func Toplevel(dir string) string {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}

// gitRemotes maps remote name to its configured fetch URL. Read from the
// config rather than `git remote -v`, which shows URLs after url.*.insteadOf
// rewriting — the configured URL is the one that says whether a remote is
// Dibbla's.
func gitRemotes(dir string) (map[string]string, error) {
	out, err := git(dir, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		// Exit 1 with no output is "no remotes", not a failure.
		if strings.TrimSpace(err.Error()) == "exit status 1" {
			return map[string]string{}, nil
		}
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, u, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name == "" || name == key {
			continue
		}
		m[name] = u
	}
	return m, nil
}

// git runs a read-only git command in dir and returns stdout; stderr is
// folded into the error.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

// gitOut runs a git command in dir with stderr passed through to the user
// (git's own messages are the best explanation of most failures).
func gitOut(dir string, stderr io.Writer, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Never stall on a password prompt: the credential helper answers for
	// Dibbla, and anything else should fail so the caller can say why.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return out.String(), nil
}

// Dirty reports whether dir's working tree or index holds anything a commit
// would pick up: modified or deleted tracked files, or untracked files that
// .gitignore does not exclude.
func Dirty(dir string) (bool, error) {
	out, err := git(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// CommitAll stages everything in the repository — tracked and untracked
// alike, .gitignore respected — and commits it with message. With allowEmpty
// a commit is written even when nothing changed (a redeploy of the same
// tree). Returns the new commit's SHA.
func CommitAll(dir, message string, allowEmpty bool, stderr io.Writer) (string, error) {
	if _, err := gitOut(dir, stderr, "add", "--all"); err != nil {
		return "", err
	}
	args := []string{"commit", "--quiet", "-m", message}
	if allowEmpty {
		args = append(args, "--allow-empty")
	}
	if _, err := gitOut(dir, stderr, args...); err != nil {
		return "", err
	}
	out, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Push sends HEAD to <remote>'s branch as a fast-forward. git's own output —
// including the "remote:" lines a Dibbla push answers with — is streamed to
// stderr and returned, so a caller can both show it and read from it.
func Push(dir, remote, branch string, stderr io.Writer) (string, error) {
	cmd := exec.Command("git", "-C", dir, "push", remote, "HEAD:refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, stderr)
	cmd.Stderr = io.MultiWriter(&buf, stderr)
	err := cmd.Run()
	if err != nil {
		return buf.String(), fmt.Errorf("git push: %w", err)
	}
	return buf.String(), nil
}
