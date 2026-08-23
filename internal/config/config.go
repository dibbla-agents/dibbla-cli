package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/joho/godotenv"
)

// contextFailure is what Load() does when the context layer cannot answer.
//
// Load() has 41 call sites and returns no error, so the alternatives were to
// widen its signature everywhere or to swallow the failure. Swallowing it is
// the one thing this proposal must not do — an unreadable config or a
// mistyped --context would silently resolve to the production endpoint. The
// exit is behind a func var so tests can observe the failure instead of dying
// on it.
var contextFailure = func(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

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

	// Context is the name of the named context these values came from, empty
	// when none was in play (env-driven usage, CI, or a machine with no
	// contexts configured yet).
	Context string
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
// org pinned by "dibbla org use" on the active context. Unlike the token it has
// no default — an empty OrgID means the request carries no X-Org-ID and the API
// falls back to the account's own default org.
//
// Named contexts (P-0011) sit strictly BELOW the environment. The env/CI
// short-circuit below returns before the context layer is reached at all, so a
// DIBBLA_API_TOKEN-driven or CI run reads no config file and no keyring — the
// behaviour is byte-for-byte what it was before contexts existed, and there is
// a test that proves it by pointing such a run at a config file that would
// error if it were parsed.
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
		// Use env only; do not read the keyring, the credentials file or the
		// context list.
		if envURL != "" {
			cfg.APIURL = envURL
		}
		return cfg
	}

	// The active context supplies the URL, the token and the org pin. A
	// legacy single-slot login is imported into a context on first run, so an
	// existing user keeps working with no re-login; see Migrate.
	//
	// The token is read from the keyring under a per-context key, falling back
	// to that context's own credentials file on hosts with no keyring service
	// (typical for Linux SSH/cloud-VM/Docker without libsecret). Same
	// semantics as the single slot it replaces — machine-wide, persists across
	// `cd`, unlike the cwd-bound `--write-env` — now one per context.
	resolved := ResolveContext()
	if resolved.Err != nil {
		// A malformed config.yaml, or a context named with --context /
		// DIBBLA_CONTEXT that does not exist. Reporting this is the whole
		// point: absorbing it would drop the user onto the production default
		// while they believe they are pointed somewhere else.
		contextFailure(resolved.Err)
		return cfg
	}
	cfg.Context = resolved.Name
	if resolved.Token != "" {
		cfg.APIToken = resolved.Token
		if resolved.APIURL != "" {
			cfg.APIURL = resolved.APIURL
		}
	} else if resolved.APIURL != "" {
		// A context with no token still says which server it points at, so
		// "not logged in" names the right instance.
		cfg.APIURL = resolved.APIURL
	}
	if envURL != "" {
		cfg.APIURL = envURL
	}

	// An explicit --org / DIBBLA_ORG_ID wins over the pinned org; only read
	// the context's pin when neither was given.
	if cfg.OrgID == "" {
		cfg.OrgID = resolved.OrgID
		cfg.OrgName = resolved.OrgName
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
