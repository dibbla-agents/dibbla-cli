package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	statusJSON       bool
	statusNoValidate bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show CLI version, API endpoint, and login state",
	Long: `Print the CLI version, the API server this CLI will talk to, and whether a
valid login is configured.

By default the configured token is validated against the resolved API URL via
POST /api/auth/v1/tokens/validate so the "logged in" line reflects the live
state of the token (revoked / expired tokens show as not logged in). Use
--no-validate to skip the network call and report only what's stored locally.

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
}

func runStatus(cmd *cobra.Command, args []string) {
	report := buildStatusReport(statusNoValidate)

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
