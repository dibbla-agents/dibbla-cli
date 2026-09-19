package gitcred

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func stubLookup(t *testing.T, creds map[string]Credential) {
	t.Helper()
	prev := lookup
	lookup = func(host string) (Credential, bool) {
		c, ok := creds[host]
		return c, ok
	}
	t.Cleanup(func() { lookup = prev })
}

func run(t *testing.T, op, input string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	err = Run(op, strings.NewReader(input), &out, &errb)
	return out.String(), errb.String(), err
}

func TestGet_AnswersTokenForDibblaHost(t *testing.T) {
	stubLookup(t, map[string]Credential{"api.dibbla.com": {Token: "tok-123"}})
	out, errs, err := run(t, "get", "protocol=https\nhost=api.dibbla.com\npath=git/acme/app.git\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "username=dibbla\npassword=tok-123\n" {
		t.Errorf("stdout = %q", out)
	}
	if errs != "" {
		t.Errorf("stderr = %q, want nothing", errs)
	}
}

func TestGet_PinnedOrgRidesInTheUsername(t *testing.T) {
	const org = "0b4e8f1a-6b2c-4d8e-9f10-1234567890ab"
	stubLookup(t, map[string]Credential{"api.dibbla.com": {Token: "tok", OrgID: org}})
	out, _, err := run(t, "get", "protocol=https\nhost=api.dibbla.com\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "username="+org+"\n") {
		t.Errorf("username should carry the org id:\n%s", out)
	}
}

func TestGet_HostIsMatchedCaseInsensitively(t *testing.T) {
	stubLookup(t, map[string]Credential{"api.dibbla.com": {Token: "tok"}})
	out, _, _ := run(t, "get", "protocol=https\nhost=API.Dibbla.com\n\n")
	if !strings.Contains(out, "password=tok") {
		t.Errorf("expected a credential for the upper-cased host, got %q", out)
	}
}

func TestGet_NotLoggedIn_TellsGitToQuitAndNamesTheFix(t *testing.T) {
	stubLookup(t, nil)
	out, errs, err := run(t, "get", "protocol=https\nhost=api.dibbla.com\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "password=") {
		t.Errorf("must not answer a credential: %q", out)
	}
	if !strings.Contains(out, "quit=1") {
		t.Errorf("git must be told to stop, not to prompt: %q", out)
	}
	if !strings.Contains(errs, "dibbla login") {
		t.Errorf("stderr should tell the user to run dibbla login: %q", errs)
	}
}

func TestGet_RefusesPlaintextHTTP(t *testing.T) {
	stubLookup(t, map[string]Credential{"api.dibbla.com": {Token: "tok"}})
	out, errs, _ := run(t, "get", "protocol=http\nhost=api.dibbla.com\n\n")
	if strings.Contains(out, "password=") {
		t.Errorf("token leaked over http: %q", out)
	}
	if !strings.Contains(out, "quit=1") || !strings.Contains(errs, "https") {
		t.Errorf("want a quit and an https hint; out=%q err=%q", out, errs)
	}
}

func TestGet_LoopbackHTTPIsAllowedForLocalStacks(t *testing.T) {
	stubLookup(t, map[string]Credential{"localhost:8080": {Token: "tok"}})
	out, _, _ := run(t, "get", "protocol=http\nhost=localhost:8080\n\n")
	if !strings.Contains(out, "password=tok") {
		t.Errorf("loopback http should be answered: %q", out)
	}
}

func TestGet_WithoutHostIsAnError(t *testing.T) {
	stubLookup(t, nil)
	if _, _, err := run(t, "get", "protocol=https\n\n"); err == nil {
		t.Error("expected an error for a request with no host")
	}
}

func TestStore_IsANoOp(t *testing.T) {
	stubLookup(t, nil)
	out, errs, err := run(t, "store", "protocol=https\nhost=api.dibbla.com\nusername=dibbla\npassword=tok\n\n")
	if err != nil || out != "" || errs != "" {
		t.Errorf("store should be silent: out=%q err=%q %v", out, errs, err)
	}
}

func TestErase_ExplainsThatTheLoginWasRejected(t *testing.T) {
	stubLookup(t, map[string]Credential{"api.dibbla.com": {Token: "tok"}})
	out, errs, err := run(t, "erase", "protocol=https\nhost=api.dibbla.com\nusername=dibbla\npassword=tok\n\n")
	if err != nil || out != "" {
		t.Errorf("erase writes nothing to git: out=%q %v", out, err)
	}
	if !strings.Contains(errs, "dibbla login") || !strings.Contains(errs, "api.dibbla.com") {
		t.Errorf("stderr should name the host and the fix: %q", errs)
	}
}

func TestRun_UnknownOperation(t *testing.T) {
	if _, _, err := run(t, "fetch", "host=x\n\n"); err == nil {
		t.Error("expected an error")
	}
}

func TestParse_StopsAtBlankLineAndRejectsGarbage(t *testing.T) {
	attrs, err := parse(strings.NewReader("host=a\npath=b=c\n\nhost=ignored\n"))
	if err != nil {
		t.Fatal(err)
	}
	if attrs["host"] != "a" || attrs["path"] != "b=c" {
		t.Errorf("attrs = %v", attrs)
	}
	if _, err := parse(strings.NewReader("no-equals\n")); err == nil {
		t.Error("a line without '=' must be rejected")
	}
}

func TestConfigKey_ScopesToSchemeHostAndGitPath(t *testing.T) {
	cases := map[string]string{
		"https://api.dibbla.com":    "credential.https://api.dibbla.com/git.helper",
		"https://API.dibbla.net/":   "credential.https://api.dibbla.net/git.helper",
		"http://localhost:8085/api": "credential.http://localhost:8085/git.helper",
	}
	for in, want := range cases {
		got, err := ConfigKey(in)
		if err != nil || got != want {
			t.Errorf("ConfigKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "api.dibbla.com", "not a url"} {
		if _, err := ConfigKey(bad); err == nil {
			t.Errorf("ConfigKey(%q) should fail", bad)
		}
	}
}

func TestHelperValue_AbsolutePathQuotedWhenNeeded(t *testing.T) {
	if got := HelperValue("/usr/local/bin/dibbla"); got != "!/usr/local/bin/dibbla git-credential" {
		t.Errorf("got %q", got)
	}
	got := HelperValue("/Applications/My Tools/dibbla")
	if got != `!"/Applications/My Tools/dibbla" git-credential` {
		t.Errorf("path with a space must be quoted: %q", got)
	}
	if runtime.GOOS == "windows" {
		got := HelperValue(`C:\Program Files\dibbla\dibbla.exe`)
		if got != `!"C:/Program Files/dibbla/dibbla.exe" git-credential` {
			t.Errorf("windows path: %q", got)
		}
	}
}

type fakeGit struct {
	calls  [][]string
	listed string // answer to --get-regexp ^credential…
	mapped string // answer to --get-regexp ^dibbla…api$
}

func stubGitConfig(t *testing.T, f *fakeGit) {
	t.Helper()
	prev := gitConfig
	gitConfig = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		if args[0] == "--get-regexp" {
			ans := f.listed
			if strings.HasPrefix(args[1], "^dibbla") {
				ans = f.mapped
			}
			if ans == "" {
				return nil, &exec.ExitError{ProcessState: exitState(t, 1)}
			}
			return []byte(ans), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { gitConfig = prev })
}

// exitState fabricates a ProcessState with the given exit code by actually
// exiting a child with it — there is no constructor for the type.
func exitState(t *testing.T, code int) *os.ProcessState {
	t.Helper()
	sh := "sh"
	args := []string{"-c", "exit " + itoa(code)}
	if runtime.GOOS == "windows" {
		sh, args = "cmd", []string{"/c", "exit " + itoa(code)}
	}
	cmd := exec.Command(sh, args...)
	_ = cmd.Run()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != code {
		t.Skip("cannot fabricate exit status on this host")
	}
	return cmd.ProcessState
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestRegister_ResetsThenAddsTheHelper(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{}
	stubGitConfig(t, f)
	if err := Register("https://api.dibbla.com"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %v", f.calls)
	}
	key := "credential.https://api.dibbla.com/git.helper"
	if got := f.calls[0]; got[0] != "--replace-all" || got[1] != key || got[2] != "" {
		t.Errorf("first call should blank the helper list: %v", got)
	}
	if got := f.calls[1]; got[0] != "--add" || got[1] != key || !strings.HasSuffix(got[2], " git-credential") || !strings.HasPrefix(got[2], "!") {
		t.Errorf("second call should add this binary: %v", got)
	}
}

func TestUnregister_RemovesOnlyDibblaHelpers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{listed: strings.Join([]string{
		"credential.https://github.com.helper !/usr/bin/gh auth git-credential",
		"credential.https://api.dibbla.com/git.helper ",
		"credential.https://api.dibbla.com/git.helper !/usr/local/bin/dibbla git-credential",
		"credential.https://api.dibbla.net/git.helper !\"/Applications/My Tools/dibbla\" git-credential",
		"credential.helper osxkeychain",
	}, "\n")}
	stubGitConfig(t, f)
	if err := Unregister(); err != nil {
		t.Fatal(err)
	}
	var unset []string
	for _, c := range f.calls[1:] {
		if c[0] == "--get-regexp" {
			continue
		}
		if c[0] != "--unset-all" {
			t.Errorf("unexpected call %v", c)
		}
		unset = append(unset, c[1])
	}
	want := map[string]bool{
		"credential.https://api.dibbla.com/git.helper": true,
		"credential.https://api.dibbla.net/git.helper": true,
	}
	if len(unset) != len(want) {
		t.Fatalf("unset = %v, want exactly %v", unset, want)
	}
	for _, k := range unset {
		if !want[k] {
			t.Errorf("must not touch %s", k)
		}
	}
}

func TestUnregister_NoEntriesIsSuccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{}
	stubGitConfig(t, f)
	if err := Unregister(); err != nil {
		t.Fatalf("no entries should not be an error: %v", err)
	}
	for _, c := range f.calls {
		if c[0] != "--get-regexp" {
			t.Errorf("nothing should be unset: %v", f.calls)
		}
	}
}

// Prod hands out clone URLs on git.dibbla.com while the login is for
// api.dibbla.com. The helper must be registered for the git host too, and
// the mapping that lets it answer for that host must be written.
func TestRegisterGitHost_RegistersTheCloneHostAndMapsItToTheAPI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{}
	stubGitConfig(t, f)
	if err := RegisterGitHost("https://git.dibbla.com/git/acme/app.git", "https://api.dibbla.com"); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range f.calls {
		keys = append(keys, c[0]+" "+c[1])
	}
	want := []string{
		"--replace-all credential.https://api.dibbla.com/git.helper",
		"--add credential.https://api.dibbla.com/git.helper",
		"--replace-all credential.https://git.dibbla.com/git.helper",
		"--add credential.https://git.dibbla.com/git.helper",
		"--replace-all dibbla.git.dibbla.com.api",
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
	if last := f.calls[len(f.calls)-1]; last[2] != "https://api.dibbla.com" {
		t.Errorf("mapping value = %q", last[2])
	}
}

// Same host (dev: api.dibbla.net serves /git itself) → plain Register, no mapping.
func TestRegisterGitHost_SameHostIsJustRegister(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{}
	stubGitConfig(t, f)
	if err := RegisterGitHost("https://api.dibbla.net/git/acme/app.git", "https://api.dibbla.net"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Errorf("calls = %v", f.calls)
	}
}

// The helper is asked for git.dibbla.com; no login names that host, but the
// mapping RegisterGitHost wrote says its login is api.dibbla.com's. The real
// lookup runs, with the login coming from the environment.
func TestGet_AnswersForAMappedGitHost(t *testing.T) {
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")
	t.Setenv("DIBBLA_API_TOKEN", "tok-prod")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prev := apiHostFor
	apiHostFor = func(h string) string {
		if h == "git.dibbla.com" {
			return "api.dibbla.com"
		}
		return ""
	}
	t.Cleanup(func() { apiHostFor = prev })
	out, _, err := run(t, "get", "protocol=https\nhost=git.dibbla.com\npath=git/acme/app.git\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "password=tok-prod") {
		t.Errorf("expected the API host's token for the mapped git host, got %q", out)
	}
	// An unmapped foreign host still gets nothing.
	out, _, _ = run(t, "get", "protocol=https\nhost=github.com\n\n")
	if !strings.Contains(out, "quit=1") {
		t.Errorf("github.com must not get the token: %q", out)
	}
}

func TestUnregister_AlsoDropsTheGitHostMapping(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fakeGit{
		listed: "credential.https://git.dibbla.com/git.helper !/usr/local/bin/dibbla git-credential",
		mapped: "dibbla.git.dibbla.com.api https://api.dibbla.com",
	}
	stubGitConfig(t, f)
	if err := Unregister(); err != nil {
		t.Fatal(err)
	}
	var unset []string
	for _, c := range f.calls {
		if c[0] == "--unset-all" {
			unset = append(unset, c[1])
		}
	}
	if strings.Join(unset, ",") != "credential.https://git.dibbla.com/git.helper,dibbla.git.dibbla.com.api" {
		t.Errorf("unset = %v", unset)
	}
}
