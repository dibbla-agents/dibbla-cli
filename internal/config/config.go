package config

import (
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/joho/godotenv"
)

const (
	// DefaultAPIURL is the default Dibbla API endpoint
	DefaultAPIURL = "https://api.dibbla.com"
	// DefaultAppURL is the default Dibbla app/auth-UI endpoint
	DefaultAppURL = "https://app.dibbla.com"
)

// FlagOrgID holds the value of the global --org flag. Cobra populates it
// during flag parsing, which happens before any command's Run, so every later
// reader — Load() and the org-header transport — sees the final value. It is a
// package-level var rather than a parameter because the org applies to the
// whole process, exactly like DIBBLA_ORG_ID does.
var FlagOrgID string

// Config holds the CLI configuration
type Config struct {
	APIURL   string
	APIToken string

	// OrgID is the organization to act as, sent as X-Org-ID on every API
	// request. Empty means "whatever org the account defaults to", which is
	// what the API does when the header is absent.
	OrgID string
	// OrgName is the display name recorded when the org was pinned. Purely
	// cosmetic — never sent to the API, and empty when the org came from
	// --org or DIBBLA_ORG_ID, where only an id is available.
	OrgName string
}

// Load reads configuration from environment variables, .env file, and OS credential store.
// In CI or when DIBBLA_API_TOKEN is set, only env is used. Otherwise stored credentials from
// "dibbla login" are used.
//
// The API URL is resolved with this precedence: DIBBLA_API_URL (preferred name)
// falls back to DIBBLA_AUTH_SERVICE_URL (the name used by the dibbla-tasks
// steprunner and desktop app when injecting env into child processes), then to
// the stored credential-store URL, then to DefaultAPIURL.
//
// The organization follows the same shape: --org, then DIBBLA_ORG_ID, then the
// org pinned by "dibbla org use". Unlike the token it has no default — an
// empty OrgID means the request carries no X-Org-ID and the API falls back to
// the account's own default org.
func Load() *Config {
	// Load .env file if it exists (ignores error if file doesn't exist)
	_ = godotenv.Load()

	envToken := os.Getenv("DIBBLA_API_TOKEN")
	envURL := os.Getenv("DIBBLA_API_URL")
	if envURL == "" {
		envURL = os.Getenv("DIBBLA_AUTH_SERVICE_URL")
	}

	envOrg := strings.TrimSpace(os.Getenv("DIBBLA_ORG_ID"))
	if FlagOrgID != "" {
		envOrg = strings.TrimSpace(FlagOrgID)
	}

	cfg := &Config{
		APIURL:   DefaultAPIURL,
		APIToken: envToken,
		OrgID:    envOrg,
	}

	if envToken != "" || platform.IsCI() {
		// Use env only; do not read keychain
		if envURL != "" {
			cfg.APIURL = envURL
		}
		return cfg
	}

	// Read order: keyring first (single read to avoid multiple OS
	// prompts), then user-level credentials file as a fallback. The
	// file is written by `dibbla login` on hosts where the OS keyring
	// is unavailable (typical for Linux SSH/cloud-VM/Docker without
	// libsecret/gnome-keyring). It mirrors keychain semantics —
	// machine-wide, persists across `cd` — rather than the cwd-bound
	// `--write-env` behavior.
	storedToken, storedURL, err := credential.GetCredentials()
	if err != nil || storedToken == "" {
		if fileToken, fileURL, ferr := credential.GetTokenFile(); ferr == nil && fileToken != "" {
			storedToken, storedURL = fileToken, fileURL
		}
	}
	if storedToken != "" {
		cfg.APIToken = storedToken
		if storedURL != "" {
			cfg.APIURL = storedURL
		}
	}
	if envURL != "" {
		cfg.APIURL = envURL
	}

	// An explicit --org / DIBBLA_ORG_ID wins over the pinned org; only read
	// the stored pin when neither was given.
	if cfg.OrgID == "" {
		storedOrgID, storedOrgName, oerr := credential.GetOrg()
		if oerr != nil || storedOrgID == "" {
			if fileOrgID, fileOrgName, ferr := credential.GetOrgFile(); ferr == nil && fileOrgID != "" {
				storedOrgID, storedOrgName = fileOrgID, fileOrgName
			}
		}
		cfg.OrgID = storedOrgID
		cfg.OrgName = storedOrgName
	}

	// Normalize: strip trailing slashes and null bytes that some OS credential
	// stores (e.g. Windows Credential Manager) may introduce.
	cfg.APIURL = strings.TrimRight(strings.TrimSuffix(cfg.APIURL, "/"), "\x00")
	cfg.OrgID = strings.TrimSpace(strings.TrimRight(cfg.OrgID, "\x00"))
	cfg.OrgName = strings.TrimSpace(strings.TrimRight(cfg.OrgName, "\x00"))

	return cfg
}

// HasToken returns true if an API token is configured
func (c *Config) HasToken() bool {
	return c.APIToken != ""
}
