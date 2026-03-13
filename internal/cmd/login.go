package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var loginAPIKey string

var loginCmd = &cobra.Command{
	Use:   "login [api_url]",
	Short: "Log in and store API credentials securely",
	Long: `Authenticate with the Dibbla API and store your token in the OS credential store.

By default uses https://api.dibbla.app. To use a different endpoint, pass it as an argument:
  dibbla login api.dibbla.net
  dibbla login https://api.dibbla.net

In CI, set DIBBLA_API_TOKEN (and optionally DIBBLA_API_URL) instead of using login.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runLogin,
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "API token (if omitted, you will be prompted)")
}

func runLogin(cmd *cobra.Command, args []string) {
	baseURL := config.DefaultAPIURL
	if len(args) > 0 {
		baseURL = normalizeAPIURL(strings.TrimSpace(args[0]))
		if baseURL == "" {
			fmt.Printf("%s Error: Invalid API URL\n", platform.Icon("❌", "[X]"))
			os.Exit(1)
		}
	}

	token := strings.TrimSpace(loginAPIKey)
	if token == "" {
		var err error
		token, err = promptAPIToken()
		if err != nil {
			fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
		token = strings.TrimSpace(token)
		if token == "" {
			fmt.Printf("%s Error: API token is required\n", platform.Icon("❌", "[X]"))
			os.Exit(1)
		}
	}

	if err := apiclient.ValidateToken(baseURL, token); err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Printf("%s Error: %s\n", platform.Icon("❌", "[X]"), apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if err := credential.SetToken(token); err != nil {
		fmt.Printf("%s Error: Token validated but failed to store credentials: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	if baseURL != config.DefaultAPIURL {
		if err := credential.SetAPIURL(baseURL); err != nil {
			fmt.Printf("%s Error: Token stored but failed to store API URL: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
	} else {
		_ = credential.DeleteAPIURL() // clear any previously stored custom URL
	}

	fmt.Printf("%s Logged in to %s\n", platform.Icon("✅", "[OK]"), baseURL)
}

// normalizeAPIURL returns a full https URL. Accepts "api.dibbla.net" or "https://api.dibbla.net".
func normalizeAPIURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		return "https://" + input
	}
	return strings.TrimSuffix(input, "/")
}

func promptAPIToken() (string, error) {
	var token string
	prompt := &survey.Password{
		Message: "API token:",
		Help:    "Get your token at https://app.dibbla.com/settings/api-tokens",
	}
	err := survey.AskOne(prompt, &token)
	return token, err
}
