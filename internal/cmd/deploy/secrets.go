package deploy

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	deploypkg "github.com/dibbla-agents/dibbla-cli/internal/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
	"github.com/spf13/cobra"
)

// secretNameRe is the server's secret-name rule (platform.md §5). Keys are
// validated against it up front so a bulk import fails closed rather than
// half-applying.
var secretNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,127}$`)

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

var secretsImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Bulk-load secrets from a .env-style file",
	Long: `Import every KEY=value from a .env-style file into the secrets store in one
shot, without a redeploy. The file is the base layer; repeatable -e KEY=value
flags override individual keys (same precedence as dibbla run / deploy).

Scope follows the usual flags: omit --deployment for org-global secrets, set
--deployment <alias> for an app, or add --service to scope to one service.

Every key is validated against the secret-name rule (^[a-zA-Z][a-zA-Z0-9_]{0,127}$)
up front: if any key is invalid the command exits without sending anything. The
server upserts each secret, so an import is idempotent and safe to re-run. Values
are never printed — output is key names and a count only.

Keep the .env file OUTSIDE your deploy directory (or in .dibblaignore): a .env in
the deploy root is a pre-deploy guardrail BLOCKER and is stripped from VCS.

Examples:
  dibbla secrets import .env
  dibbla secrets import ../secrets/.env.prod -d shop
  dibbla secrets import .env -d shop -s web -e API_KEY=override
  dibbla secrets import .env -d shop --dry-run`,
	Args: cobra.ExactArgs(1),
	Run:  runSecretsImport,
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
	secretsImportDeployment string
	secretsImportService    string
	secretsImportEnv        []string
	secretsImportDryRun     bool
)

func init() {
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	secretsCmd.AddCommand(secretsImportCmd)

	secretsListCmd.Flags().StringVarP(&secretsDeployment, "deployment", "d", "", "List secrets for this deployment only (omit for global)")
	secretsListCmd.Flags().StringVarP(&secretsListService, "service", "s", "", "Scope to a single service in the deployment (requires -d)")
	secretsSetCmd.Flags().StringVarP(&secretsSetDeployment, "deployment", "d", "", "Attach secret to this deployment (omit for global)")
	secretsSetCmd.Flags().StringVarP(&secretsSetService, "service", "s", "", "Scope secret to a single service (requires -d)")
	secretsGetCmd.Flags().StringVarP(&secretsGetDeployment, "deployment", "d", "", "Get deployment-scoped secret")
	secretsGetCmd.Flags().StringVarP(&secretsGetService, "service", "s", "", "Scope to a single service entry (requires -d)")
	secretsDeleteCmd.Flags().StringVarP(&secretsDeleteDeployment, "deployment", "d", "", "Delete deployment-scoped secret")
	secretsDeleteCmd.Flags().StringVarP(&secretsDeleteService, "service", "s", "", "Scope delete to a single service entry (requires -d)")
	secretsDeleteCmd.Flags().BoolVarP(&secretsDeleteYes, "yes", "y", false, "Skip confirmation prompt")
	secretsImportCmd.Flags().StringVarP(&secretsImportDeployment, "deployment", "d", "", "Import into this deployment (omit for global)")
	secretsImportCmd.Flags().StringVarP(&secretsImportService, "service", "s", "", "Scope to a single service (requires -d)")
	secretsImportCmd.Flags().StringArrayVarP(&secretsImportEnv, "env", "e", nil, "Override a single KEY=value on top of the file (repeatable)")
	secretsImportCmd.Flags().BoolVar(&secretsImportDryRun, "dry-run", false, "List the keys that would be set (no values, no network)")
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

func runSecretsImport(cmd *cobra.Command, args []string) {
	if !requireServiceWithDeployment(os.Stderr, secretsImportDeployment, secretsImportService) {
		os.Exit(1)
	}

	// File is the base layer; -e flags override individual keys. A missing or
	// malformed file (or a bad -e flag) fails here, before any network call.
	envMap, err := deploypkg.MergeEnvFileAndFlags(args[0], secretsImportEnv)
	if err != nil {
		fmt.Printf("%s %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	if len(envMap) == 0 {
		fmt.Printf("%s No secrets found in %q.\n", platform.Icon("❌", "[X]"), args[0])
		os.Exit(1)
	}

	// Stable, sorted key order for deterministic output (and a deterministic
	// import sequence so a re-run after a mid-loop failure is predictable).
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Validate every key up front; fail closed if any is invalid so we never
	// half-apply a file.
	var invalid []string
	for _, k := range keys {
		if !secretNameRe.MatchString(k) {
			invalid = append(invalid, k)
		}
	}
	if len(invalid) > 0 {
		fmt.Printf("%s Invalid secret name(s): %s\n", platform.Icon("❌", "[X]"), strings.Join(invalid, ", "))
		fmt.Printf("  Names must match %s\n", secretNameRe.String())
		fmt.Println("  Nothing was imported.")
		os.Exit(1)
	}

	scope := scopeLabel(secretsImportDeployment, secretsImportService)

	if secretsImportDryRun {
		fmt.Printf("%s Dry run — would import %d secret(s) into %s:\n", platform.Icon("🌱", "[>]"), len(keys), scope)
		for _, k := range keys {
			fmt.Printf("  %s\n", k)
		}
		fmt.Println("\nNo secrets were written (--dry-run).")
		return
	}

	cfg := config.Load()
	requireToken(cfg)

	fmt.Printf("%s Importing %d secret(s) into %s...\n", platform.Icon("🌱", "[>]"), len(keys), scope)
	fmt.Println()

	var done []string
	for _, k := range keys {
		_, err := secrets.CreateSecret(cfg.APIURL, cfg.APIToken, k, envMap[k], secretsImportDeployment, secretsImportService)
		if err != nil {
			fmt.Printf("%s Failed at %s after %d of %d: %v\n", platform.Icon("❌", "[X]"), k, len(done), len(keys), err)
			if len(done) > 0 {
				fmt.Printf("  Imported before the failure: %s\n", strings.Join(done, ", "))
			}
			fmt.Println("  Re-running the import is safe (the server upserts).")
			os.Exit(1)
		}
		done = append(done, k)
	}

	fmt.Printf("%s imported %d secret(s) into %s: %s\n", platform.Icon("✅", "[OK]"), len(done), scope, strings.Join(done, ", "))
}
