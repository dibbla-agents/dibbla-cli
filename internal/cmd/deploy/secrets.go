package deploy

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets (global or per-deployment)",
	Long:  `Create, list, get, and delete secrets. Omit --deployment for global secrets; set it to scope to an app.`,
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secrets",
	Long:  `List secrets. Use --deployment <alias> for a single deployment; omit for global secrets only.`,
	Run:   runSecretsList,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <name> [value]",
	Short: "Create or update a secret",
	Long:  `Set a secret by name. If value is omitted, it is read from stdin (e.g. echo "secret" | dibbla secrets set API_KEY). Use --deployment to attach to an app.`,
	Args:  cobra.RangeArgs(1, 2),
	Run:   runSecretsSet,
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a secret's value",
	Long:  `Get a secret by name. Use --deployment for a deployment-scoped secret.`,
	Args:  cobra.ExactArgs(1),
	Run:   runSecretsGet,
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a secret",
	Long:  `Delete a secret by name. Use --deployment for a deployment-scoped secret.`,
	Args:  cobra.ExactArgs(1),
	Run:   runSecretsDelete,
}

var (
	secretsDeployment       string
	secretsSetDeployment    string
	secretsGetDeployment    string
	secretsDeleteDeployment string
	secretsListService      string
	secretsSetService       string
	secretsGetService       string
	secretsDeleteService    string
	secretsDeleteYes        bool
)

func init() {
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)

	secretsListCmd.Flags().StringVarP(&secretsDeployment, "deployment", "d", "", "List secrets for this deployment only (omit for global)")
	secretsListCmd.Flags().StringVarP(&secretsListService, "service", "s", "", "Scope to a single service in the deployment (requires -d)")
	secretsSetCmd.Flags().StringVarP(&secretsSetDeployment, "deployment", "d", "", "Attach secret to this deployment (omit for global)")
	secretsSetCmd.Flags().StringVarP(&secretsSetService, "service", "s", "", "Scope secret to a single service (requires -d)")
	secretsGetCmd.Flags().StringVarP(&secretsGetDeployment, "deployment", "d", "", "Get deployment-scoped secret")
	secretsGetCmd.Flags().StringVarP(&secretsGetService, "service", "s", "", "Scope to a single service entry (requires -d)")
	secretsDeleteCmd.Flags().StringVarP(&secretsDeleteDeployment, "deployment", "d", "", "Delete deployment-scoped secret")
	secretsDeleteCmd.Flags().StringVarP(&secretsDeleteService, "service", "s", "", "Scope delete to a single service entry (requires -d)")
	secretsDeleteCmd.Flags().BoolVarP(&secretsDeleteYes, "yes", "y", false, "Skip confirmation prompt")
}

// requireServiceWithDeployment fails when --service is set without --deployment.
// Returns the cobra-friendly error so callers can return it from RunE if any.
func requireServiceWithDeployment(stderr io.Writer, deployment, service string) bool {
	if service != "" && deployment == "" {
		fmt.Fprintf(stderr, "%s --service requires --deployment (-d)\n", platform.Icon("❌", "[X]"))
		return false
	}
	return true
}

func runSecretsList(cmd *cobra.Command, args []string) {
	if !requireServiceWithDeployment(os.Stderr, secretsDeployment, secretsListService) {
		os.Exit(1)
	}
	fmt.Printf("%s Retrieving secrets...\n", platform.Icon("🌱", "[>]"))
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	list, err := secrets.ListSecrets(cfg.APIURL, cfg.APIToken, secretsDeployment, secretsListService)
	if err != nil {
		fmt.Printf("%s Failed to list secrets: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	scope := scopeLabel(secretsDeployment, secretsListService)
	if list.Total == 0 {
		fmt.Printf("No secrets found (%s).\n", scope)
		return
	}
	fmt.Printf("Found %d secret(s) (%s):\n", list.Total, scope)
	fmt.Println()
	fmt.Printf("%-25s %-20s %-12s %s\n", "NAME", "DEPLOYMENT", "SERVICE", "UPDATED")
	fmt.Printf("%-25s %-20s %-12s %s\n", "----", "-----------", "-------", "------")
	for _, s := range list.Secrets {
		dep := s.DeploymentAlias
		if dep == "" {
			dep = "(global)"
		}
		svc := s.ServiceName
		if svc == "" {
			svc = "(all)"
		}
		fmt.Printf("%-25s %-20s %-12s %s\n", s.Name, dep, svc, s.UpdatedAt)
	}
}

// scopeLabel summarizes the deployment+service scope for human messages.
func scopeLabel(deployment, service string) string {
	switch {
	case deployment != "" && service != "":
		return "deployment " + deployment + ", service " + service
	case deployment != "":
		return "deployment " + deployment
	default:
		return "global"
	}
}

func runSecretsSet(cmd *cobra.Command, args []string) {
	if !requireServiceWithDeployment(os.Stderr, secretsSetDeployment, secretsSetService) {
		os.Exit(1)
	}
	name := args[0]
	value := ""
	if len(args) == 2 {
		value = args[1]
	} else {
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("%s Failed to read stdin: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
		value = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	if value == "" {
		fmt.Printf("%s Error: secret value is required (provide as second argument or via stdin)\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	fmt.Printf("%s Setting secret '%s'...\n", platform.Icon("🌱", "[>]"), name)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	res, err := secrets.CreateSecret(cfg.APIURL, cfg.APIToken, name, value, secretsSetDeployment, secretsSetService)
	if err != nil {
		fmt.Printf("%s Failed to set secret: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", platform.Icon("✅", "[OK]"), res.Message)
	fmt.Printf("  Secret: %s\n", res.Secret.Name)
	if res.Secret.DeploymentAlias != "" {
		fmt.Printf("  Deployment: %s\n", res.Secret.DeploymentAlias)
	}
	if res.Secret.ServiceName != "" {
		fmt.Printf("  Service:    %s\n", res.Secret.ServiceName)
	}
}

func runSecretsGet(cmd *cobra.Command, args []string) {
	if !requireServiceWithDeployment(os.Stderr, secretsGetDeployment, secretsGetService) {
		os.Exit(1)
	}
	name := args[0]

	cfg := config.Load()
	requireToken(cfg)

	res, err := secrets.GetSecret(cfg.APIURL, cfg.APIToken, name, secretsGetDeployment, secretsGetService)
	if err != nil {
		fmt.Printf("%s Failed to get secret: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Print(res.Value)
	if !strings.HasSuffix(res.Value, "\n") {
		fmt.Println()
	}
}

func runSecretsDelete(cmd *cobra.Command, args []string) {
	if !requireServiceWithDeployment(os.Stderr, secretsDeleteDeployment, secretsDeleteService) {
		os.Exit(1)
	}
	name := args[0]
	scope := scopeLabel(secretsDeleteDeployment, secretsDeleteService)

	fmt.Printf("%s Attempting to delete secret '%s' (%s)...\n", platform.Icon("🗑️", "[DEL]"), name, scope)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	if !secretsDeleteYes {
		if !askConfirm(fmt.Sprintf("Are you sure you want to delete secret '%s'?", name)) {
			fmt.Println("Deletion cancelled.")
			os.Exit(0)
		}
	}

	del, err := secrets.DeleteSecret(cfg.APIURL, cfg.APIToken, name, secretsDeleteDeployment, secretsDeleteService)
	if err != nil {
		fmt.Printf("%s Failed to delete secret: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", platform.Icon("✅", "[OK]"), del.Message)
}
