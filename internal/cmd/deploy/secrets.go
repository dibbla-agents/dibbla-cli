package deploy

import (
	"errors"
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
	isatty "github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// secretNameRe is the server's secret-name rule (platform.md §5). Keys are
// validated against it up front so a bulk import fails closed rather than
// half-applying.
var secretNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,127}$`)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets (global or per-deployment)",
	Long: `Create, list and delete secrets. Omit --deployment for global secrets; set it to scope to an app.

A secret is write-only: Dibbla never hands out its value — not to this CLI, not
to an API, not to an AI assistant. The app gets it in its environment when it
runs. To change one, set a new value: 'secrets set' in your own terminal, or
'secrets request' for a link where the value is entered in the browser — the
way an AI assistant asks for one without ever seeing it.`,
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
	Long: `Set a secret by name. Leave the value out: at a terminal you are asked for it
and it is not echoed (paste it, then press Enter on an empty line); otherwise
stdin is read to its end (a pipe, or a file: < key.pem). A value typed as an
argument still works, but it lands in your shell history — and, when an AI
assistant runs the command, in its transcript — so the CLI warns about it.

An AI assistant does not run this with a value: it runs 'dibbla secrets request',
and you enter the value in the browser. Use --deployment to attach to an app;
it works before the app's first deploy.`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSecretsSet,
}

// secretsGetCmd stays registered so `secrets get` explains itself instead of
// answering "unknown command": a secret's value cannot be read back
// (DIB-1337), and the server refuses it for every older CLI too.
var secretsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Removed: a secret's value cannot be read back",
	Long: `A secret is write-only: Dibbla never hands out its value. The app gets it in
its environment when it runs.

  dibbla secrets list [-d <alias>]           # which secrets exist
  dibbla secrets set <name> [-d <alias>]     # change one`,
	Args: cobra.MaximumNArgs(1),
	Run:  runSecretsGet,
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
  dibbla secrets import .env -d shop -s web -e STRIPE_MODE=test
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
	fd := os.Stdin.Fd()
	os.Exit(runSecretsSetCore(os.Stdout, os.Stderr, secretsSetInput{
		Args: args, Deployment: secretsSetDeployment, Service: secretsSetService,
		Stdin: os.Stdin, StdinIsTerminal: isatty.IsTerminal(fd),
		ReadHiddenLine: func() ([]byte, error) { return term.ReadPassword(int(fd)) },
		Set: func(name, value, deployment, service string) (*secrets.SecretCreateResponse, error) {
			cfg := config.Load()
			requireToken(cfg)
			return secrets.CreateSecret(cfg.APIURL, cfg.APIToken, name, value, deployment, service)
		},
	}))
}

type secretsSetInput struct {
	Args                []string
	Deployment, Service string
	// Stdin is read to its end when it is not a terminal (a pipe, < file).
	Stdin           io.Reader
	StdinIsTerminal bool
	// ReadHiddenLine reads one line from the terminal without echoing it.
	ReadHiddenLine func() ([]byte, error)
	Set            func(name, value, deployment, service string) (*secrets.SecretCreateResponse, error)
}

// runSecretsSetCore is `dibbla secrets set`. It never writes the value to
// stdout or stderr. Returns the exit code.
func runSecretsSetCore(stdout, stderr io.Writer, in secretsSetInput) int {
	bad := platform.Icon("❌", "[X]")
	if !requireServiceWithDeployment(stderr, in.Deployment, in.Service) {
		return 1
	}
	name := in.Args[0]
	var value string
	switch {
	case len(in.Args) == 2:
		value = in.Args[1]
		// It works, and it has already cost something: the value is in the
		// shell's history and, if an assistant ran this, in its transcript.
		fmt.Fprintf(stderr, "%s The value was given on the command line, so it is now in your shell history — and, if an AI assistant ran this command, in its transcript.\n", platform.Icon("⚠️", "[!]"))
		fmt.Fprintf(stderr, "  Next time leave it out and paste it when asked (dibbla secrets set %s%s), or use 'dibbla secrets request %s%s' to enter it in the browser.\n", name, scopeFlags(in.Deployment, in.Service), name, scopeFlags(in.Deployment, in.Service))
	case in.StdinIsTerminal:
		// A person at a terminal: the value is read without echo, a line at
		// a time, until an empty line. Not just the first line: a pasted PEM
		// key or JSON key file would otherwise leave every line after the
		// first in the terminal's buffer, where the shell reads it as
		// commands — and keeps it in history.
		fmt.Fprintf(stderr, "Value for %s (input hidden): paste it, then press Enter on an empty line: ", name)
		var lines []string
		for {
			line, err := in.ReadHiddenLine()
			if err != nil && !errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr)
				fmt.Fprintf(stderr, "%s Failed to read the value: %v\n", bad, err)
				return 1
			}
			text := strings.TrimRight(string(line), "\r")
			if text != "" {
				lines = append(lines, text)
			}
			if text == "" || err != nil {
				break
			}
		}
		fmt.Fprintln(stderr)
		value = strings.TrimSpace(strings.Join(lines, "\n"))
	default:
		raw, err := io.ReadAll(in.Stdin)
		if err != nil {
			fmt.Fprintf(stderr, "%s Failed to read stdin: %v\n", bad, err)
			return 1
		}
		value = strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	}
	if value == "" {
		fmt.Fprintf(stderr, "%s Error: secret value is required (paste it when asked, or pipe it on stdin; or let the person enter it: dibbla secrets request %s)\n", bad, name)
		return 1
	}

	fmt.Fprintf(stdout, "%s Setting secret '%s'...\n", platform.Icon("🌱", "[>]"), name)
	fmt.Fprintln(stdout)

	res, err := in.Set(name, value, in.Deployment, in.Service)
	if err != nil {
		fmt.Fprintf(stderr, "%s Failed to set secret: %v\n", bad, err)
		return 1
	}

	fmt.Fprintf(stdout, "%s %s\n", platform.Icon("✅", "[OK]"), res.Message)
	fmt.Fprintf(stdout, "  Secret: %s\n", res.Secret.Name)
	if res.Secret.DeploymentAlias != "" {
		fmt.Fprintf(stdout, "  Deployment: %s\n", res.Secret.DeploymentAlias)
	}
	if res.Secret.ServiceName != "" {
		fmt.Fprintf(stdout, "  Service:    %s\n", res.Secret.ServiceName)
	}
	return 0
}

// scopeFlags renders " -d shop -s web" for a hint that repeats the command.
func scopeFlags(deployment, service string) string {
	out := ""
	if deployment != "" {
		out += " -d " + deployment
	}
	if service != "" {
		out += " -s " + service
	}
	return out
}

func runSecretsGet(cmd *cobra.Command, args []string) {
	os.Exit(refuseSecretValueRead(os.Stderr, "secrets get"))
}

// refuseSecretValueRead is the answer to every command that used to print a
// secret's value. Exit 1: the request cannot succeed, now or on retry.
func refuseSecretValueRead(w io.Writer, command string) int {
	fmt.Fprintf(w, "%s %s: a secret's value cannot be read back — secrets are write-only on Dibbla.\n", platform.Icon("❌", "[X]"), command)
	fmt.Fprintln(w, "  The app gets its secrets in its environment when it runs.")
	fmt.Fprintln(w, "  See which exist: dibbla secrets list [-d <alias>]   Change one: dibbla secrets set <name> [-d <alias>]")
	return 1
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
		ok, err := askConfirm(fmt.Sprintf("Are you sure you want to delete secret '%s'?", name))
		if err != nil {
			os.Exit(refuseUnconfirmable(os.Stderr, fmt.Sprintf("deleting secret '%s'", name)))
		}
		if !ok {
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
	// The config/token lookup is deferred into a closure so --dry-run and every
	// pre-flight failure stay strictly offline: nothing reads credentials or
	// touches the network unless a secret is actually about to be written.
	setSecret := func(name, value string) error {
		cfg := config.Load()
		requireToken(cfg)
		_, err := secrets.CreateSecret(cfg.APIURL, cfg.APIToken, name, value, secretsImportDeployment, secretsImportService)
		return err
	}
	os.Exit(runSecretsImportCore(os.Stdout, os.Stderr, args[0], secretsImportEnv,
		secretsImportDeployment, secretsImportService, secretsImportDryRun, setSecret))
}

// runSecretsImportCore is the testable inner implementation of
// `secrets import`. Returns the exit code. Its only side effects are writes to
// the given writers and calls to setSecret — which it makes exactly zero of
// when the input is rejected or --dry-run is set.
//
// It never writes a secret value to either writer: output is key names and
// counts only.
func runSecretsImportCore(stdout, stderr io.Writer, file string, envFlags []string,
	deployment, service string, dryRun bool, setSecret func(name, value string) error) int {

	if !requireServiceWithDeployment(stderr, deployment, service) {
		return 1
	}

	// File is the base layer; -e flags override individual keys. A missing or
	// malformed file (or a bad -e flag) fails here, before any network call.
	envMap, err := deploypkg.MergeEnvFileAndFlags(file, envFlags)
	if err != nil {
		fmt.Fprintf(stderr, "%s %v\n", platform.Icon("❌", "[X]"), err)
		return 1
	}
	if len(envMap) == 0 {
		fmt.Fprintf(stderr, "%s No secrets found in %q.\n", platform.Icon("❌", "[X]"), file)
		return 1
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
		fmt.Fprintf(stderr, "%s Invalid secret name(s): %s\n", platform.Icon("❌", "[X]"), strings.Join(invalid, ", "))
		fmt.Fprintf(stderr, "  Names must match %s\n", secretNameRe.String())
		fmt.Fprintln(stderr, "  Nothing was imported.")
		return 1
	}

	scope := scopeLabel(deployment, service)

	if dryRun {
		fmt.Fprintf(stdout, "%s Dry run — would import %d secret(s) into %s:\n", platform.Icon("🌱", "[>]"), len(keys), scope)
		for _, k := range keys {
			fmt.Fprintf(stdout, "  %s\n", k)
		}
		fmt.Fprintln(stdout, "\nNo secrets were written (--dry-run).")
		return 0
	}

	fmt.Fprintf(stdout, "%s Importing %d secret(s) into %s...\n", platform.Icon("🌱", "[>]"), len(keys), scope)
	fmt.Fprintln(stdout)

	var done []string
	for _, k := range keys {
		if err := setSecret(k, envMap[k]); err != nil {
			fmt.Fprintf(stderr, "%s Failed at %s after %d of %d: %v\n", platform.Icon("❌", "[X]"), k, len(done), len(keys), err)
			if len(done) > 0 {
				fmt.Fprintf(stderr, "  Imported before the failure: %s\n", strings.Join(done, ", "))
			}
			fmt.Fprintln(stderr, "  Re-running the import is safe (the server upserts).")
			return 1
		}
		done = append(done, k)
	}

	fmt.Fprintf(stdout, "%s imported %d secret(s) into %s: %s\n", platform.Icon("✅", "[OK]"), len(done), scope, strings.Join(done, ", "))
	return 0
}
