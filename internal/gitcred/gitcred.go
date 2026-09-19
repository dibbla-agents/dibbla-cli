// Package gitcred is the git credential helper behind `dibbla git-credential`.
//
// It answers git's credential protocol (gitcredentials(7)) for Dibbla's git
// host only, out of the same storage `dibbla login` writes: the keyring or the
// per-context credentials file, or DIBBLA_API_TOKEN when that is what the
// process was given. Nothing is stored anywhere new — the helper is a read
// path onto the login, which is what lets `git push` and `git pull` work
// without a token ever landing in .git/config or ~/.git-credentials.
//
// The helper is registered in the user-level git config for the git host of
// the API URL a login pointed at, so other remotes (GitHub, GitLab, a
// self-hosted Gitea) never see it. Registration is the same shape `gh auth
// setup-git` uses: an absolute path to this binary in the `!cmd` form, which
// git runs the same way on Windows, macOS and Linux.
package gitcred

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
)

// GitPathPrefix is where deploy-api serves smart-HTTP git under the API host.
// It is part of the config key so the helper is only consulted for clone URLs
// and never for anything else that might one day live on the same host.
const GitPathPrefix = "/git"

// DefaultUsername is what the helper answers as the username when no
// organization is pinned. The proxy ignores it; git just needs one.
const DefaultUsername = "dibbla"

// ConfigKey returns the user-level git config key that scopes this helper to
// the git host of apiURL, e.g. credential.https://api.dibbla.com/git.helper.
func ConfigKey(apiURL string) (string, error) {
	u, err := parseAPIURL(apiURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("credential.%s://%s%s.helper", u.Scheme, strings.ToLower(u.Host), GitPathPrefix), nil
}

func parseAPIURL(apiURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("not an absolute API URL: %q", apiURL)
	}
	return u, nil
}

// HelperValue is the git config value that runs this binary as a credential
// helper: "!<path> git-credential". The path is absolute so the helper works
// from any shell git happens to be launched from, even one where dibbla is
// not on PATH (a GUI git client, an IDE). Backslashes become forward slashes
// on Windows and the path is quoted when it has whitespace — git hands the
// `!` form to its own sh, which reads those the same on every platform.
func HelperValue(exe string) string {
	if runtime.GOOS == "windows" {
		exe = strings.ReplaceAll(exe, `\`, `/`)
	}
	if strings.ContainsAny(exe, " \t'") {
		exe = `"` + exe + `"`
	}
	return "!" + exe + " git-credential"
}

// gitConfig runs `git config --global` with args. Indirected for tests.
var gitConfig = func(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"config", "--global"}, args...)...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// Register makes git consult this binary for the git host of apiURL. The
// helper list for that URL is first reset with an empty entry so that a
// system-wide helper (osxkeychain, manager-core, store) is neither consulted
// before us nor asked to cache the token afterwards. Idempotent.
func Register(apiURL string) error {
	key, err := ConfigKey(apiURL)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate dibbla binary: %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed or not on PATH")
	}
	if _, err := gitConfig("--replace-all", key, ""); err != nil {
		return fmt.Errorf("git config %s: %w", key, err)
	}
	if _, err := gitConfig("--add", key, HelperValue(exe)); err != nil {
		return fmt.Errorf("git config %s: %w", key, err)
	}
	return nil
}

// RegisterGitHost makes git consult this binary for the host a clone URL
// actually points at. In dev the git host IS the API host
// (https://api.dibbla.net/git/…) and this is Register(apiURL); in prod the
// server hands out https://git.dibbla.com/git/… while the API is
// api.dibbla.com, and a helper registered for the API host is never asked.
// So the helper is registered for the clone URL's origin too, and a
// `dibbla.<git-host>.api` entry records which API the login lives under, so
// the helper can answer for a host no context names. Idempotent.
func RegisterGitHost(cloneURL, apiURL string) error {
	if err := Register(apiURL); err != nil {
		return err
	}
	gu, err := parseAPIURL(cloneURL)
	if err != nil {
		return fmt.Errorf("clone URL: %w", err)
	}
	au, err := parseAPIURL(apiURL)
	if err != nil {
		return err
	}
	if strings.EqualFold(gu.Host, au.Host) {
		return nil
	}
	origin := gu.Scheme + "://" + strings.ToLower(gu.Host)
	key, err := ConfigKey(origin)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate dibbla binary: %w", err)
	}
	if _, err := gitConfig("--replace-all", key, ""); err != nil {
		return fmt.Errorf("git config %s: %w", key, err)
	}
	if _, err := gitConfig("--add", key, HelperValue(exe)); err != nil {
		return fmt.Errorf("git config %s: %w", key, err)
	}
	if _, err := gitConfig("--replace-all", apiForHostKey(gu.Host), au.Scheme+"://"+strings.ToLower(au.Host)); err != nil {
		return fmt.Errorf("git config %s: %w", apiForHostKey(gu.Host), err)
	}
	return nil
}

// apiForHostKey is the git config key that maps a git host to the API host
// whose login answers for it: [dibbla "git.dibbla.com"] api = https://api.dibbla.com
func apiForHostKey(gitHost string) string {
	return "dibbla." + strings.ToLower(gitHost) + ".api"
}

// apiHostFor answers the API host recorded for a git host by RegisterGitHost,
// or "" when the git host is not one we mapped. Indirected for tests.
var apiHostFor = func(gitHost string) string {
	out, err := gitConfig("--get", apiForHostKey(gitHost))
	if err != nil {
		return ""
	}
	return hostOf(strings.TrimSpace(string(out)))
}

// IsRegistered reports whether the user-level git config already routes the
// git host of apiURL to this helper.
func IsRegistered(apiURL string) bool {
	key, err := ConfigKey(apiURL)
	if err != nil {
		return false
	}
	out, err := gitConfig("--get-all", key)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), " git-credential")
}

// Unregister removes every credential.<url>.helper entry that points at a
// dibbla binary, for any host. Used by `dibbla uninstall`, which does not know
// which API URLs were ever logged in to. Entries for other helpers are left
// alone. A missing git, or no entries, is success.
func Unregister() error {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	out, err := gitConfig("--get-regexp", `^credential\..*\.helper$`)
	if err != nil {
		// Exit 1 means no match; anything else is reported.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("git config --get-regexp: %w", err)
	}
	keys := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok || !isDibblaHelper(value) {
			continue
		}
		keys[key] = true
	}
	for key := range keys {
		// Drop the helper and the empty reset entry that came with it, so
		// the host falls back to whatever helper the user has otherwise.
		if _, err := gitConfig("--unset-all", key); err != nil {
			return fmt.Errorf("git config --unset-all %s: %w", key, err)
		}
	}
	// And the git-host → API rows RegisterGitHost wrote; they mean nothing
	// without the helper. No match is exit 1 and fine.
	if out, err := gitConfig("--get-regexp", `^dibbla\..*\.api$`); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if key, _, ok := strings.Cut(line, " "); ok && key != "" {
				if _, err := gitConfig("--unset-all", key); err != nil {
					return fmt.Errorf("git config --unset-all %s: %w", key, err)
				}
			}
		}
	}
	return nil
}

// isDibblaHelper recognises a value Register wrote: "!<path-to-dibbla>
// git-credential". The binary name is checked, not just the subcommand —
// `!gh auth git-credential` ends the same way and must survive an uninstall.
func isDibblaHelper(value string) bool {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "!") || !strings.HasSuffix(v, " git-credential") {
		return false
	}
	exe := strings.Trim(strings.TrimSuffix(v[1:], " git-credential"), `"' `)
	base := exe[strings.LastIndexAny(exe, `/\`)+1:]
	return strings.HasPrefix(base, "dibbla")
}

// Credential is what the helper answers with for a host.
type Credential struct {
	Token string
	OrgID string
}

// lookup finds the login for a git host. Indirected for tests.
//
// The active context (or DIBBLA_API_TOKEN) is tried first because its org pin
// is the one the user just chose with `dibbla org use`. Only when the
// requested host is a different Dibbla instance are the other contexts
// scanned, so someone logged in to both production and a customer instance
// pushes to each with the right credential.
var lookup = func(host string) (Credential, bool) {
	if c, ok := lookupAPIHost(host); ok {
		return c, true
	}
	// A git host that is not the API host (prod: git.dibbla.com for
	// api.dibbla.com) was mapped when the folder was cloned or linked.
	if api := apiHostFor(host); api != "" && api != host {
		return lookupAPIHost(api)
	}
	return Credential{}, false
}

// lookupAPIHost finds the login whose API URL has exactly this host.
func lookupAPIHost(host string) (Credential, bool) {
	cfg := config.Load()
	if cfg.HasToken() && hostOf(cfg.APIURL) == host {
		return Credential{Token: cfg.APIToken, OrgID: cfg.OrgID}, true
	}
	ccfg, err := contextcfg.Load()
	if err != nil {
		return Credential{}, false
	}
	for _, name := range ccfg.Names() {
		r := config.ResolveContextNamed(name)
		if r.Err == nil && r.Token != "" && hostOf(r.APIURL) == host {
			return Credential{Token: r.Token, OrgID: r.OrgID}, true
		}
	}
	return Credential{}, false
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// Run serves one credential-protocol operation: get, store or erase. Input
// is the key=value block git writes on stdin; output goes to stdout in the
// same format. Human-facing messages go to stderr, which git passes through
// to the terminal.
func Run(op string, stdin io.Reader, stdout, stderr io.Writer) error {
	attrs, err := parse(stdin)
	if err != nil {
		return err
	}
	host := strings.ToLower(attrs["host"])
	switch op {
	case "get":
		return get(host, attrs["protocol"], stdout, stderr)
	case "store":
		// The token already lives in the login store; nothing to remember.
		return nil
	case "erase":
		// git calls erase after the server answered 401 to the credential
		// we handed out: the login is expired or revoked. The stored token
		// is left in place — the next `dibbla login` replaces it — but the
		// user must hear that git's "Authentication failed" means "log in
		// again", not "wrong password".
		if host != "" {
			fmt.Fprintf(stderr, "dibbla: %s rejected your login (expired or revoked). Run `dibbla login` and retry.\n", host)
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q (want get, store or erase)", op)
	}
}

func get(host, protocol string, stdout, stderr io.Writer) error {
	if host == "" {
		return errors.New("no host in credential request")
	}
	cred, ok := lookup(host)
	if !ok {
		// quit=1 stops git from trying other helpers or prompting for a
		// password that could never be right; the line above it is the
		// reason git shows before its own "credential helper told us to
		// quit".
		fmt.Fprintf(stderr, "dibbla: not logged in to %s. Run `dibbla login` and retry.\n", host)
		fmt.Fprintln(stdout, "quit=1")
		return nil
	}
	if protocol != "" && protocol != "https" && !isLoopback(host) {
		// Never hand a bearer token to a plaintext remote.
		fmt.Fprintf(stderr, "dibbla: refusing to send your login over %s to %s; use https.\n", protocol, host)
		fmt.Fprintln(stdout, "quit=1")
		return nil
	}
	username := DefaultUsername
	if cred.OrgID != "" {
		// The org rides in the username: git has no other channel for it,
		// and the proxy reads a UUID-shaped Basic username as X-Org-ID so a
		// push lands in the pinned organization's repo.
		username = cred.OrgID
	}
	fmt.Fprintf(stdout, "username=%s\npassword=%s\n", username, cred.Token)
	return nil
}

func isLoopback(host string) bool {
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h, "]") {
		h = h[:i]
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]"
}

// parse reads git's key=value block up to the first blank line or EOF.
func parse(r io.Reader) (map[string]string, error) {
	attrs := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("malformed credential line %q", line)
		}
		attrs[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return attrs, nil
}
