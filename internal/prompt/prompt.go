package prompt

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
)

// AskProjectName prompts the user for a project name
func AskProjectName() string {
	var name string
	prompt := &survey.Input{
		Message: "Project name:",
	}
	survey.AskOne(prompt, &name, survey.WithValidator(survey.Required))
	return strings.TrimSpace(name)
}

// AskAPIToken prompts the user for their API token
// Returns empty string if skipped
func AskAPIToken() string {
	var token string
	prompt := &survey.Password{
		Message: "API Token (from app.dibbla.com/settings/api-keys):",
		Help:    "Press Enter to skip - you can add it to .env later",
	}
	survey.AskOne(prompt, &token)
	token = strings.TrimSpace(token)

	if token == "" {
		fmt.Println("  ⚠️  Warning: No token provided. Add SERVER_API_TOKEN to .env before running.")
		return ""
	}

	// Validate token format
	if !preflight.ValidateToken(token) {
		fmt.Println("  ⚠️  Warning: Token should start with 'ak_'. Using as-is.")
	}

	return token
}

// AskIncludeFrontend asks if the user wants to include the frontend
func AskIncludeFrontend() bool {
	var include bool
	prompt := &survey.Confirm{
		Message: "Include frontend?",
		Default: false,
	}
	survey.AskOne(prompt, &include)
	return include
}

// AskConfirm asks a yes/no question with default yes
func AskConfirm(message string) bool {
	var confirm bool
	prompt := &survey.Confirm{
		Message: message,
		Default: true,
	}
	survey.AskOne(prompt, &confirm)
	return confirm
}

