package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
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
  API URL: DIBBLA_API_URL > DIBBLA_AUTH_SERVICE_URL > keyring > credentials file > default
  Token:   DIBBLA_API_TOKEN > keyring > credentials file > none

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
	Validated       bool   `json:"validated"`
	LoggedIn        bool   `json:"logged_in"`
	ValidationError string `json:"validation_error,omitempty"`
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

	r := statusReport{
		Version:         Version,
		APIURL:          apiURL,
		APIURLSource:    apiURLSource,
		TokenConfigured: token != "",
		TokenSource:     tokenSource,
	}

	if !r.TokenConfigured || noValidate {
		return r
	}

	r.Validated = true
	if err := apiclient.ValidateToken(apiURL, token); err != nil {
		r.LoggedIn = false
		r.ValidationError = err.Error()
		return r
	}
	r.LoggedIn = true
	return r
}

// resolveAPIURLWithSource mirrors config.Load's URL precedence and reports
// where the chosen value came from. Kept inline rather than refactoring
// config.Load so the precedence change blast radius stays in one file.
func resolveAPIURLWithSource() (url, source string) {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_API_URL")); v != "" {
		return normalizeURL(v), "env (DIBBLA_API_URL)"
	}
	if v := strings.TrimSpace(os.Getenv("DIBBLA_AUTH_SERVICE_URL")); v != "" {
		return normalizeURL(v), "env (DIBBLA_AUTH_SERVICE_URL)"
	}
	// Honor the same env-only short-circuit as config.Load: when
	// DIBBLA_API_TOKEN is set or we're in CI, the keyring/file are not
	// consulted, so reporting their stored URLs would be misleading.
	envToken := os.Getenv("DIBBLA_API_TOKEN")
	if envToken == "" && !platform.IsCI() {
		if storedToken, storedURL, err := credential.GetCredentials(); err == nil && storedToken != "" && storedURL != "" {
			return normalizeURL(storedURL), "keyring"
		}
		if fileToken, fileURL, err := credential.GetTokenFile(); err == nil && fileToken != "" && fileURL != "" {
			return normalizeURL(fileURL), "credentials file"
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
		// — keyring is not consulted. Report nothing rather than silently
		// reading credentials that won't be used at runtime.
		return "", "none"
	}
	if t, err := credential.GetToken(); err == nil && t != "" {
		return t, "keyring"
	}
	if t, _, err := credential.GetTokenFile(); err == nil && t != "" {
		return t, "credentials file"
	}
	return "", "none"
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
	fmt.Printf("API:     %s  (%s)\n", r.APIURL, r.APIURLSource)
	if r.TokenConfigured {
		fmt.Printf("Token:   configured  (source: %s)\n", r.TokenSource)
	} else {
		fmt.Printf("Token:   not configured\n")
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
