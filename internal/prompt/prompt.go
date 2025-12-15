package prompt

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
)

// HostingType represents the type of Dibbla hosting
type HostingType int

const (
	HostingCloud HostingType = iota
	HostingSelfHosted
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

// AskHostingType prompts the user to select between Dibbla Cloud and Self-Hosted
func AskHostingType() HostingType {
	var selection string
	prompt := &survey.Select{
		Message: "Hosting type:",
		Options: []string{"Dibbla Cloud", "Self-Hosted"},
		Default: "Dibbla Cloud",
	}
	survey.AskOne(prompt, &selection)

	if selection == "Self-Hosted" {
		return HostingSelfHosted
	}
	return HostingCloud
}

// AskGrpcAddress prompts the user for their self-hosted gRPC server address
func AskGrpcAddress() string {
	var address string
	prompt := &survey.Input{
		Message: "gRPC server address:",
		Default: "localhost:9090",
		Help:    "The address of your self-hosted Dibbla gRPC server",
	}
	survey.AskOne(prompt, &address)
	return strings.TrimSpace(address)
}

// AskUseTLS prompts the user if they want to use TLS for the gRPC connection
func AskUseTLS() bool {
	var useTLS bool
	prompt := &survey.Confirm{
		Message: "Use TLS for gRPC connection?",
		Default: false,
		Help:    "Enable TLS if your self-hosted server requires encrypted connections",
	}
	survey.AskOne(prompt, &useTLS)
	return useTLS
}

// AskAPIToken prompts the user for their API token
// Returns empty string if skipped
func AskAPIToken(isSelfHosted bool) string {
	var token string
	var message string
	if isSelfHosted {
		message = "API Token (from your self-hosted dashboard):"
	} else {
		message = "API Token (from app.dibbla.com/settings/api-keys):"
	}

	prompt := &survey.Password{
		Message: message,
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

