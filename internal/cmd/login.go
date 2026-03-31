package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/auth"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var loginAPIKey string

var loginCmd = &cobra.Command{
	Use:   "login [api_url]",
	Short: "Log in and store API credentials securely",
	Long: `Authenticate with the Dibbla API and store your token in the OS credential store.

By default uses https://api.dibbla.com. To use a different endpoint, pass it as an argument:
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
		token, err = acquireToken(baseURL)
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

// acquireToken presents the user with a choice of login methods and returns an API token.
func acquireToken(baseURL string) (string, error) {
	interactive := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !interactive {
		return "", fmt.Errorf("non-interactive terminal detected; use --api-key flag or set DIBBLA_API_TOKEN environment variable")
	}

	const (
		optBrowser  = "Log in with browser"
		optAPIToken = "Paste an API token"
	)

	var method string
	prompt := &survey.Select{
		Message: "How would you like to log in?",
		Options: []string{optBrowser, optAPIToken},
	}
	if err := survey.AskOne(prompt, &method); err != nil {
		return "", err
	}

	switch method {
	case optBrowser:
		return browserLogin(baseURL)
	default:
		return promptAPIToken()
	}
}

// browserLogin performs the browser-based OAuth login flow.
func browserLogin(apiBaseURL string) (string, error) {
	// Derive the app URL for the auth UI.
	appURL := config.DefaultAppURL
	if apiBaseURL != config.DefaultAPIURL {
		derived, err := auth.DeriveAppURL(apiBaseURL)
		if err != nil {
			return "", fmt.Errorf("cannot determine app URL for %s: %w\nUse 'Paste an API token' instead", apiBaseURL, err)
		}
		appURL = derived
	}

	state, err := auth.GenerateState()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	port, resultCh, shutdown := auth.StartCallbackServer(ctx, state)
	defer shutdown()

	loginURL := auth.BuildLoginURL(appURL, port, state)

	fmt.Printf("%s Opening browser for login...\n", platform.Icon("🌐", "[>]"))

	if err := auth.OpenBrowser(loginURL); err != nil {
		// Browser didn't open — try clipboard, then print URL.
		if clipErr := auth.CopyToClipboard(loginURL); clipErr == nil {
			fmt.Printf("  %s Login URL copied to clipboard!\n", platform.Icon("📋", "[*]"))
		}
		fmt.Printf("  If the browser didn't open, visit:\n  %s\n", loginURL)
	}

	fmt.Println()
	fmt.Println("Waiting for browser login... (press Ctrl+C to cancel)")

	result := <-resultCh
	if result.Err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("login timed out after 5 minutes; try again or use --api-key")
		}
		return "", result.Err
	}

	fmt.Printf("%s Browser login successful! Creating API token...\n", platform.Icon("✅", "[OK]"))

	apiToken, err := auth.ExchangeJWTForAPIToken(apiBaseURL, result.Token)
	if err != nil {
		return "", fmt.Errorf("failed to create API token: %w", err)
	}

	return apiToken, nil
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
