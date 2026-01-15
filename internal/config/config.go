package config

import (
	"os"

	"github.com/joho/godotenv"
)

const (
	// DefaultAPIURL is the default Dibbla API endpoint
	DefaultAPIURL = "https://api.dibbla.app"
)

// Config holds the CLI configuration
type Config struct {
	APIURL   string
	APIToken string
}

// Load reads configuration from environment variables and .env file
// Environment variables take precedence over .env file values
func Load() *Config {
	// Load .env file if it exists (ignores error if file doesn't exist)
	_ = godotenv.Load()

	cfg := &Config{
		APIURL:   DefaultAPIURL,
		APIToken: os.Getenv("DIBBLA_API_TOKEN"),
	}

	// Override API URL if set in environment
	if url := os.Getenv("DIBBLA_API_URL"); url != "" {
		cfg.APIURL = url
	}

	return cfg
}

// HasToken returns true if an API token is configured
func (c *Config) HasToken() bool {
	return c.APIToken != ""
}
