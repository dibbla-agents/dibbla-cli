package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dibbla-agents/dibbla-cli/internal/create"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.AddCommand(goWorkerCmd)
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Dibbla project",
	Long:  `Create a new Dibbla project from a template.`,
}

var goWorkerCmd = &cobra.Command{
	Use:   "go-worker [name]",
	Short: "Create a new Go worker project",
	Long: `Create a new Dibbla Go worker project from the starter template.

Examples:
  dibbla create go-worker my-worker
  dibbla create go-worker`,
	Args: cobra.MaximumNArgs(1),
	Run:  runGoWorker,
}

func runGoWorker(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Dibbla Go Worker Generator\n", platform.Icon("🚀", ">>"))
	fmt.Println()

	// Run pre-flight checks
	fmt.Println("Checking prerequisites...")
	preflight.CheckGo()
	fmt.Println()

	// Get project name (from arg or prompt)
	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	} else {
		projectName = prompt.AskProjectName()
	}

	// Check if directory exists
	if preflight.DirectoryExists(projectName) {
		fmt.Printf("%s Error: Directory '%s' already exists\n", platform.Icon("❌", "[X]"), projectName)
		os.Exit(1)
	}

	// Show full path and confirm
	fullPath, _ := filepath.Abs(projectName)
	fmt.Printf("\n%s Project will be created at:\n   %s\n\n", platform.Icon("📁", "[DIR]"), fullPath)

	if !prompt.AskConfirm("Continue?") {
		fmt.Println("Cancelled.")
		os.Exit(0)
	}

	// Get hosting type
	hostingType := prompt.AskHostingType()
	isSelfHosted := hostingType == prompt.HostingSelfHosted

	// Self-hosted configuration
	var grpcAddress string
	var useTLS bool
	if isSelfHosted {
		grpcAddress = prompt.AskGrpcAddress()
		useTLS = prompt.AskUseTLS()
	}

	// Get API token (with context-aware message)
	apiToken := prompt.AskAPIToken(isSelfHosted)

	// Get frontend preference
	includeFrontend := prompt.AskIncludeFrontend()

	fmt.Println()
	fmt.Println("Creating project...")

	// Create the project
	config := create.ProjectConfig{
		Name:            projectName,
		Token:           apiToken,
		IncludeFrontend: includeFrontend,
		SelfHosted:      isSelfHosted,
		GrpcAddress:     grpcAddress,
		UseTLS:          useTLS,
	}

	if err := create.GoWorker(config); err != nil {
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	// Success message
	fmt.Println()
	fmt.Printf("%s Ready! Run your worker:\n", platform.Icon("🎉", "[*]"))
	fmt.Printf("   cd %s\n", projectName)
	if apiToken == "" {
		fmt.Println("   # Don't forget to add your API token to .env first!")
	}
	fmt.Println("   go run ./cmd/worker")

	if includeFrontend {
		fmt.Println()
		fmt.Println("   Frontend (in a separate terminal):")
		fmt.Printf("   cd %s/frontend && npm run dev\n", projectName)
	}
}
