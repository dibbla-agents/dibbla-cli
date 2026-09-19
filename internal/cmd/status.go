package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/gitlink"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	statusJSON       bool
	statusNoValidate bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show CLI version, API endpoint, login state — and how a linked folder relates to Dibbla",
	Long: `Print the CLI version, the API server this CLI will talk to, and whether a
valid login is configured.

By default the configured token is validated against the resolved API URL via
POST /api/auth/v1/tokens/validate so the "logged in" line reflects the live
state of the token (revoked / expired tokens show as not logged in). Use
--no-validate to skip the network call and report only what's stored locally.

Run inside a folder linked to an app (dibbla clone / dibbla link), status also
shows the app and org the folder is connected to, how many commits it is ahead
of or behind Dibbla's main (after a fetch), and whether the commit the app runs
is this folder's HEAD. --no-validate skips the fetch and the running-commit
lookup too.

The "source" annotations show where each value came from. Resolution order
matches the rest of the CLI:
  Context: --context > DIBBLA_CONTEXT > the context selected with "dibbla context use"
  API URL: DIBBLA_API_URL > DIBBLA_AUTH_SERVICE_URL > the context's URL > default
  Token:   DIBBLA_API_TOKEN > the context's token (keyring, then its credentials file) > none
  Org:     --org > DIBBLA_ORG_ID > the context's org pin > account default

Exit codes:
  0  logged in (or --no-validate and a token is configured)
  3  not logged in / token invalid
  1  unexpected error (network, malformed response)`,
	Args: cobra.NoArgs,
	Run:  runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Emit machine-readable JSON instead of human text")
	statusCmd.Flags().BoolVar(&statusNoValidate, "no-validate", false, "Skip the live token validation request")
}

type statusReport struct {
	Version         string `json:"version"`
	APIURL          string `json:"api_url"`
	APIURLSource    string `json:"api_url_source"`
	TokenConfigured bool   `json:"token_configured"`
	TokenSource     string `json:"token_source"`
	OrgID           string `json:"org_id,omitempty"`
	OrgName         string `json:"org_name,omitempty"`
	OrgSource       string `json:"org_source"`
	// Context names which login target produced the values above, and
	// ContextCount how many are configured — so `dibbla status --json` answers
	// "which server am I on, and are there others" in one call.
	Context         string `json:"context,omitempty"`
	ContextCount    int    `json:"context_count"`
	Validated       bool   `json:"validated"`
	LoggedIn        bool   `json:"logged_in"`
	ValidationError string `json:"validation_error,omitempty"`
	// Plan fields (P-0027) come from the same validate call — absent under
	// --no-validate (no network) and on orgs/installs without a plan.
	Plan        string `json:"plan,omitempty"`
	TrialEndsAt string `json:"trial_ends_at,omitempty"`
	// Folder describes the git repo status was run in when that repo has a
	// Dibbla remote (DIB-905); nil elsewhere.
	Folder *folderReport `json:"folder,omitempty"`
}

// folderReport is the linked-folder half of `dibbla status`: which app the
// folder is connected to and how its commits relate to Dibbla's.
type folderReport struct {
	Path   string `json:"path"`
	App    string `json:"app"`
	Org    string `json:"org"`
	Remote string `json:"remote"`
	Branch string `json:"branch,omitempty"`
	// HeadSHA is the local HEAD; Ahead/Behind count commits against
	// <remote>/<branch> after a fetch (or the last fetch under
	// --no-validate, in which case Fetched is false).
	HeadSHA string `json:"head_sha,omitempty"`
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
	Fetched bool   `json:"fetched"`
	Dirty   bool   `json:"dirty"`
	// RunningSHA is the commit the app runs on Dibbla, when the API said;
	// RunningIsHead is true when that is the local HEAD.
	RunningSHA    string `json:"running_sha,omitempty"`
	RunningIsHead bool   `json:"running_is_head"`
	Error         string `json:"error,omitempty"`
}

func runStatus(cmd *cobra.Command, args []string) {
	report := buildStatusReport(statusNoValidate)
	report.Folder = buildFolderReport(".", report.APIURL, resolvedToken(), statusNoValidate || !report.TokenConfigured)

	if statusJSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
	} else {
		printStatusHuman(report)
	}

	if !report.TokenConfigured {
		os.Exit(3)
	}
	if report.Validated && !report.LoggedIn {
		os.Exit(3)
	}
}

// buildStatusReport resolves the API URL + token (each with source), and
// optionally validates the token. Pulled out of runStatus so tests can
// drive it without touching the cobra command or os.Exit.
func buildStatusReport(noValidate bool) statusReport {
	apiURL, apiURLSource := resolveAPIURLWithSource()
	token, tokenSource := resolveTokenWithSource()
	orgID, orgName, orgSource := resolveOrgWithSource()

	r := statusReport{
		Version:         Version,
		APIURL:          apiURL,
		APIURLSource:    apiURLSource,
		TokenConfigured: token != "",
		TokenSource:     tokenSource,
		OrgID:           orgID,
		OrgName:         orgName,
		OrgSource:       orgSource,
	}
	if !envOnly() {
		resolved := config.ResolveContext()
		r.Context, r.ContextCount = resolved.Name, resolved.Count
	}

	if !r.TokenConfigured || noValidate {
		return r
	}

	r.Validated = true
	// The pinned org rides along as the override so the validated answer —
	// including the plan — is about the org shown on the Org: line.
	info, err := apiclient.ValidateTokenDetailed(apiURL, token, orgID)
	if err != nil {
		r.LoggedIn = false
		r.ValidationError = err.Error()
		return r
	}
	if info != nil {
		r.Plan = info.OrgPlan
		r.TrialEndsAt = info.OrgTrialEndsAt
	}
	r.LoggedIn = true
	return r
}

// resolvedToken is the token the folder check may use — the same one the
// login line was resolved from.
func resolvedToken() string {
	t, _ := resolveTokenWithSource()
	return t
}

// buildFolderReport describes dir when it sits in a git repo with a Dibbla
// remote. offline skips the fetch and the API lookup, so the counts are as of
// the last fetch and the running commit is unknown.
func buildFolderReport(dir, apiURL, token string, offline bool) *folderReport {
	top := gitlink.Toplevel(dir)
	if top == "" {
		return nil
	}
	remote, target, ok := gitlink.FindDibblaRemote(top)
	if !ok {
		return nil
	}
	fr := &folderReport{Path: top, App: target.App, Org: target.Org, Remote: remote}
	if st, err := gitlink.Inspect(top, ""); err == nil {
		fr.Branch = st.Branch
	}
	branch := fr.Branch
	if branch == "" {
		branch = "main"
	}
	sync, err := gitlink.Compare(top, remote, branch, !offline, io.Discard)
	if err != nil {
		// A fetch that fails (offline, login rejected) still leaves the
		// last-fetched counts usable.
		sync, err = gitlink.Compare(top, remote, branch, false, io.Discard)
		if err != nil {
			fr.Error = err.Error()
			return fr
		}
		fr.Fetched = false
	} else {
		fr.Fetched = !offline
	}
	fr.HeadSHA, fr.Ahead, fr.Behind, fr.Dirty = sync.HeadSHA, sync.Ahead, sync.Behind, sync.Dirty

	if offline || token == "" {
		return fr
	}
	fr.RunningSHA = lookupRunningSHA(apiURL, token, target.App)
	fr.RunningIsHead = fr.RunningSHA != "" && fr.RunningSHA == fr.HeadSHA
	return fr
}

// lookupRunningSHA asks the API which commit the app runs. Indirected so
// tests can answer without a server. Empty when unknown.
var lookupRunningSHA = func(apiURL, token, alias string) string {
	dep, _, err := apps.GetApp(apiURL, token, alias)
	if err != nil || dep == nil {
		return ""
	}
	return dep.CommitSHA
}

// envOnly reports whether config.Load would take its env-only short-circuit and
// return before reading any local store. Named once and used by all three
// resolvers below, because getting this condition subtly different in one of
// them is exactly how they drift.
func envOnly() bool {
	return os.Getenv("DIBBLA_API_TOKEN") != "" || platform.IsCI()
}

// resolveAPIURLWithSource mirrors config.Load's URL precedence and reports
// where the chosen value came from.
//
// These three resolvers duplicate Load()'s ladder rather than calling it,
// because Load returns values without saying where they came from and the whole
// job of `dibbla status` is to say. That duplication is a standing hazard —
// after named contexts there are four ladders in this codebase that must agree
// — so it is pinned by a test that drives status and Load through identical
// environments and requires the same answer, rather than by this comment.
func resolveAPIURLWithSource() (url, source string) {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_API_URL")); v != "" {
		return normalizeURL(v), "env (DIBBLA_API_URL)"
	}
	if v := strings.TrimSpace(os.Getenv("DIBBLA_AUTH_SERVICE_URL")); v != "" {
		return normalizeURL(v), "env (DIBBLA_AUTH_SERVICE_URL)"
	}
	// Honor the same env-only short-circuit as config.Load: when
	// DIBBLA_API_TOKEN is set or we're in CI, no local store is consulted, so
	// reporting a stored URL would be misleading.
	if !envOnly() {
		if r := config.ResolveContext(); r.Err == nil && r.APIURL != "" {
			return normalizeURL(r.APIURL), "context " + r.Name
		}
	}
	return config.DefaultAPIURL, "default"
}

func resolveTokenWithSource() (token, source string) {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_API_TOKEN")); v != "" {
		return v, "env (DIBBLA_API_TOKEN)"
	}
	if platform.IsCI() {
		// CI without DIBBLA_API_TOKEN: same short-circuit as config.Load
		// — no local store is consulted. Report nothing rather than silently
		// reading credentials that won't be used at runtime.
		return "", "none"
	}
	r := config.ResolveContext()
	if r.Err != nil || r.Token == "" {
		return "", "none"
	}
	return r.Token, fmt.Sprintf("%s (context %s)", r.TokenStore, r.Name)
}

// resolveOrgWithSource mirrors config.Load's org precedence and reports where
// the value came from. An empty id is not a missing value: it means no
// organization was selected and the API will use the account's default — and,
// concretely, that no X-Org-ID header is sent at all.
//
// The pin is read from the ACTIVE CONTEXT rather than from a machine-wide slot
// (P-0011 Part C): an organization id only means anything on the server that
// issued it.
func resolveOrgWithSource() (orgID, orgName, source string) {
	if v := strings.TrimSpace(config.FlagOrgID); v != "" {
		return v, "", "flag (--org)"
	}
	if v := strings.TrimSpace(os.Getenv("DIBBLA_ORG_ID")); v != "" {
		return v, "", "env (DIBBLA_ORG_ID)"
	}
	if envOnly() {
		return "", "", "none (account default)"
	}
	if r := config.ResolveContext(); r.Err == nil && r.OrgID != "" {
		return r.OrgID, r.OrgName, "context " + r.Name
	}
	return "", "", "none (account default)"
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return strings.TrimRight(strings.TrimSuffix(s, "/"), "\x00")
}

func printStatusHuman(r statusReport) {
	ok := platform.Icon("✅", "[OK]")
	bad := platform.Icon("❌", "[X]")
	warn := platform.Icon("⚠", "[!]")

	fmt.Printf("Dibbla CLI %s\n", r.Version)
	switch {
	case r.Context != "" && r.ContextCount > 1:
		fmt.Printf("Context: %s  (%d configured — `dibbla context list`)\n", r.Context, r.ContextCount)
	case r.Context != "":
		fmt.Printf("Context: %s\n", r.Context)
	case r.ContextCount > 0:
		fmt.Printf("Context: none selected  (%d configured — `dibbla context use <name>`)\n", r.ContextCount)
	}
	fmt.Printf("API:     %s  (%s)\n", r.APIURL, r.APIURLSource)
	if r.TokenConfigured {
		fmt.Printf("Token:   configured  (source: %s)\n", r.TokenSource)
	} else {
		fmt.Printf("Token:   not configured\n")
	}
	switch {
	case r.OrgID == "":
		fmt.Printf("Org:     account default (none selected)\n")
	case r.OrgName != "":
		fmt.Printf("Org:     %s  (%s, source: %s)\n", r.OrgName, r.OrgID, r.OrgSource)
	default:
		fmt.Printf("Org:     %s  (source: %s)\n", r.OrgID, r.OrgSource)
	}

	if r.Plan != "" {
		if r.Plan == "trial" && r.TrialEndsAt != "" {
			fmt.Printf("Plan:    %s (ends %s)\n", r.Plan, r.TrialEndsAt)
		} else {
			fmt.Printf("Plan:    %s\n", r.Plan)
		}
	}

	if r.Folder != nil {
		printFolderHuman(r.Folder, ok, warn)
	}

	switch {
	case !r.TokenConfigured:
		fmt.Printf("Status:  %s not logged in — run `dibbla login`\n", bad)
	case !r.Validated:
		fmt.Printf("Status:  %s token configured (validation skipped)\n", warn)
	case r.LoggedIn:
		fmt.Printf("Status:  %s logged in\n", ok)
	default:
		fmt.Printf("Status:  %s token rejected: %s\n", bad, r.ValidationError)
		fmt.Printf("         re-authenticate with `dibbla login`\n")
	}
}

// printFolderHuman renders the linked-folder lines. "N ahead / M behind" is
// the answer to the question a person in a linked folder has: is what I have
// what Dibbla has?
func printFolderHuman(f *folderReport, ok, warn string) {
	fmt.Printf("Folder:  %s\n", f.Path)
	fmt.Printf("App:     %s  (org %s, remote %s)\n", f.App, f.Org, f.Remote)
	if f.Error != "" {
		fmt.Printf("Sync:    %s %s\n", warn, f.Error)
		return
	}
	var rel string
	switch {
	case f.Ahead == 0 && f.Behind == 0:
		rel = "in sync with Dibbla"
	case f.Behind == 0:
		rel = fmt.Sprintf("%d commit(s) ahead of Dibbla", f.Ahead)
	case f.Ahead == 0:
		rel = fmt.Sprintf("%d commit(s) behind Dibbla — git pull to catch up", f.Behind)
	default:
		rel = fmt.Sprintf("%d commit(s) ahead, %d behind Dibbla — histories have diverged", f.Ahead, f.Behind)
	}
	if !f.Fetched {
		rel += " (as of the last fetch)"
	}
	if f.Dirty {
		rel += "; uncommitted changes on disk"
	}
	fmt.Printf("Sync:    %s\n", rel)
	switch {
	case f.RunningSHA == "":
		fmt.Printf("Running: unknown\n")
	case f.RunningIsHead:
		fmt.Printf("Running: %s %s — the commit the app runs is this folder's HEAD\n", ok, short(f.RunningSHA))
	default:
		fmt.Printf("Running: %s %s — not this folder's HEAD (%s)\n", warn, short(f.RunningSHA), short(f.HeadSHA))
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
