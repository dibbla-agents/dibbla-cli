package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/create"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.AddCommand(goWorkerCmd)
	createCmd.AddCommand(mcpCmd)
	mcpCmd.Flags().StringVar(&mcpGrpcAddress, "grpc-address", "",
		"where the deployed server connects, host:port (default: grpc.<domain>:443, from the Dibbla API URL you are signed in to)")
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

var mcpGrpcAddress string

var mcpCmd = &cobra.Command{
	Use:   "mcp [name]",
	Short: "Create an MCP server project: your own tools at an MCP address of their own",
	Long: `Create an MCP server project from the starter template.

The project is a small Go tool server with one example function and a
dibbla.yaml that publishes it as an MCP. Deploy it and its functions are MCP
tools at mcp.<domain>/platform/servers/<name>, for the people the app's access
list admits (every member of your organization by default):

  dibbla create mcp my-tools
  cd my-tools
  dibbla deploy                   the developer role is enough
  dibbla mcp server my-tools      prints the client configuration

<name> is the project directory, the app's alias and the last part of the MCP
address: ` + create.MCPNameRule + `.
It has to be a tool server name no other app on the installation uses; the
deploy output says so if it is taken.

You write functions, not MCP: the platform serves the protocol, the login and
the access check. The project's README describes the flow and how
auth.access_policy (all_members | invite_only) decides who gets the tools.

Needs git. Go is only needed to run or test the server on your own machine;
the platform builds the image.`,
	Example: `  dibbla create mcp my-tools
  dibbla create mcp my-tools --grpc-address grpc.example.com:443`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE:         runCreateMCP,
}

func runCreateMCP(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()

	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		name = prompt.AskProjectName()
	}
	if err := create.ValidateMCPName(name); err != nil {
		return err
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%q already exists here; choose another name or remove it", name)
	}

	grpcAddress := mcpGrpcAddress
	if grpcAddress == "" {
		apiURL := config.Load().APIURL
		derived, err := create.GrpcAddressFromAPIURL(apiURL)
		if err != nil {
			return fmt.Errorf("cannot tell where the server should connect from the API URL %q (%v): pass --grpc-address grpc.<domain>:443", apiURL, err)
		}
		grpcAddress = derived
	}

	if err := create.MCP(create.MCPConfig{Name: name, GrpcAddress: grpcAddress}); err != nil {
		return err
	}

	fullPath, _ := filepath.Abs(name)
	fmt.Fprintf(w, "%s Created %s\n", platform.Icon("✅", "[OK]"), fullPath)
	fmt.Fprintf(w, "   tool server name  %s\n", name)
	fmt.Fprintf(w, "   connects to       %s\n", grpcAddress)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Next:")
	fmt.Fprintf(w, "   cd %s\n", name)
	fmt.Fprintln(w, "   dibbla deploy")
	fmt.Fprintf(w, "   dibbla mcp server %s\n", name)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "README.md in the project describes the flow, adding tools, and who may use the MCP.")
	return nil
}
