package config

import (
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/joho/godotenv"
)

const (
	// DefaultAPIURL is the default Dibbla API endpoint
	DefaultAPIURL = "https://api.dibbla.com"
)

// Config holds the CLI configuration
type Config struct {
	APIURL   string
	APIToken string
}

// isCI returns true when running in a CI environment (env vars take precedence, no keychain).
func isCI() bool {
	return os.Getenv("CI") != "" ||
		os.Getenv("GITHUB_ACTIONS") != "" ||
		os.Getenv("GITLAB_CI") != "" ||
		os.Getenv("JENKINS_HOME") != "" ||
		os.Getenv("BUILDKITE") != ""
}

// Load reads configuration from environment variables, .env file, and OS credential store.
// In CI or when DIBBLA_API_TOKEN is set, only env is used. Otherwise stored credentials from
// "dibbla login" are used.
func Load() *Config {
	// Load .env file if it exists (ignores error if file doesn't exist)
	_ = godotenv.Load()

	envToken := os.Getenv("DIBBLA_API_TOKEN")
	envURL := os.Getenv("DIBBLA_API_URL")

	cfg := &Config{
		APIURL:   DefaultAPIURL,
		APIToken: envToken,
	}

	if envToken != "" || isCI() {
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
