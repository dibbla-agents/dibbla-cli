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

// Config holds the CLI configuration
type Config struct {
	APIURL   string
	APIToken string
}

// Load reads configuration from environment variables, .env file, and OS credential store.
// In CI or when DIBBLA_API_TOKEN is set, only env is used. Otherwise stored credentials from
// "dibbla login" are used.
//
// The API URL is resolved with this precedence: DIBBLA_API_URL (preferred name)
// falls back to DIBBLA_AUTH_SERVICE_URL (the name used by the dibbla-tasks
// steprunner and desktop app when injecting env into child processes), then to
// the stored credential-store URL, then to DefaultAPIURL.
func Load() *Config {
	// Load .env file if it exists (ignores error if file doesn't exist)
	_ = godotenv.Load()

	envToken := os.Getenv("DIBBLA_API_TOKEN")
	envURL := os.Getenv("DIBBLA_API_URL")
	if envURL == "" {
		envURL = os.Getenv("DIBBLA_AUTH_SERVICE_URL")
	}

	cfg := &Config{
		APIURL:   DefaultAPIURL,
		APIToken: envToken,
	}

	if envToken != "" || platform.IsCI() {
		// Use env only; do not read keychain
		if envURL != "" {
			cfg.APIURL = envURL
		}
		return cfg
	}

	// Try credential store from "dibbla login" (single keyring read to avoid multiple OS prompts)
	if storedToken, storedURL, err := credential.GetCredentials(); err == nil && storedToken != "" {
		cfg.APIToken = storedToken
		if storedURL != "" {
			cfg.APIURL = storedURL
		}
	}
	if envURL != "" {
		cfg.APIURL = envURL
	}

	// Normalize: strip trailing slashes and null bytes that some OS credential
	// stores (e.g. Windows Credential Manager) may introduce.
	cfg.APIURL = strings.TrimRight(strings.TrimSuffix(cfg.APIURL, "/"), "\x00")

	return cfg
}

// HasToken returns true if an API token is configured
func (c *Config) HasToken() bool {
	return c.APIToken != ""
}
