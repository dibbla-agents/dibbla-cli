package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/orgs"
	"github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var orgListJSON bool

var orgCmd = &cobra.Command{
	Use:     "org",
	Aliases: []string{"orgs", "organization"},
	Short:   "Show and switch the organization the CLI acts as",
	Long: `Show and switch the organization the CLI acts as.

Your API token is tied to your user, not to one organization, so switching
needs no new login: the chosen organization is sent with each request as
X-Org-ID and the API verifies your membership before honoring it.

With no organization selected the API uses your account's default — the same
one the console opens on.

Precedence, highest first:
  --org <id>          just this invocation
  DIBBLA_ORG_ID       this shell
  dibbla org use ...  stored, until you change it
  (none)              your account's default organization`,
	Args: cobra.NoArgs,
	Run:  runOrgList,
}

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organizations you belong to",
	Args:  cobra.NoArgs,
	Run:   runOrgList,
}

var orgUseCmd = &cobra.Command{
	Use:   "use <name|slug|id>",
	Short: "Act as the given organization from now on",
	Long: `Act as the given organization from now on.

Accepts a name, a slug, or an id. The selection is stored next to your login
credentials, so it persists across invocations and directories until you run
"dibbla org clear" or log out.`,
	Args: cobra.ExactArgs(1),
	Run:  runOrgUse,
}

var orgClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Go back to your account's default organization",
	Args:  cobra.NoArgs,
	Run:   runOrgClear,
}

func init() {
	orgListCmd.Flags().BoolVar(&orgListJSON, "json", false, "Emit machine-readable JSON instead of a table")
	orgCmd.Flags().BoolVar(&orgListJSON, "json", false, "Emit machine-readable JSON instead of a table")
	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgUseCmd)
	orgCmd.AddCommand(orgClearCmd)
}

// requireLogin returns a config with a token, or exits 3 the way the other
// authenticated commands do.
func requireLogin() *config.Config {
	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}
	return cfg
}

func exitAPIError(err error) {
	if apiErr, ok := err.(*apiclient.APIError); ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(apiErr.Message))
		os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func runOrgList(cmd *cobra.Command, args []string) {
	cfg := requireLogin()

	list, err := orgs.List(cfg.APIURL, cfg.APIToken, false)
	if err != nil {
		exitAPIError(err)
	}

	if orgListJSON {
		type entry struct {
			orgs.Org
			Active bool `json:"active"`
		}
		out := make([]entry, 0, len(list))
		for _, o := range list {
			out = append(out, entry{Org: o, Active: strings.EqualFold(o.ID, cfg.OrgID)})
		}
		if err := output.PrintJSON(out); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(list) == 0 {
		fmt.Println("You do not belong to any organization.")
		return
	}

	rows := make([][]string, 0, len(list))
	for _, o := range list {
		marker := ""
		if strings.EqualFold(o.ID, cfg.OrgID) {
			marker = platform.Icon("→", "*")
		}
		rows = append(rows, []string{marker, o.Name, o.Slug, o.Role, o.ID})
	}
	output.PrintTable([]string{"", "NAME", "SLUG", "ROLE", "ID"}, rows)

	fmt.Println()
	if cfg.OrgID == "" {
		fmt.Println("No organization selected — using your account's default.")
		fmt.Println("Select one with `dibbla org use <name>`.")
		return
	}
	// A pinned org that isn't in the list means the membership went away
	// since it was pinned. Every other command will 403 until it's changed,
	// so say that here rather than letting the next deploy explain it.
	found := false
	for _, o := range list {
		if strings.EqualFold(o.ID, cfg.OrgID) {
			found = true
			break
		}
	}
	if !found {
		fmt.Printf("%s Selected organization %s is not in this list — you may have been removed from it.\n",
			platform.Icon("⚠", "[!]"), cfg.OrgID)
		fmt.Println("  Pick another with `dibbla org use <name>`, or run `dibbla org clear`.")
	}
}

func runOrgUse(cmd *cobra.Command, args []string) {
	cfg := requireLogin()

	list, err := orgs.List(cfg.APIURL, cfg.APIToken, false)
	if err != nil {
		exitAPIError(err)
	}

	org, err := orgs.Resolve(list, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(4)
	}

	// Same fallback shape as login: keyring first, user-level credentials
	// file when this host has no keyring service.
	stored := "keychain"
	if err := credential.SetOrg(org.ID, org.Name); err != nil {
		if !credential.IsKeyringUnavailable(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if ferr := credential.SetOrgFile(org.ID, org.Name); ferr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", ferr)
			os.Exit(1)
		}
		stored = credential.TokenFilePath()
	}

	fmt.Printf("%s Now acting as %s (%s) — role %s\n",
		platform.Icon("✅", "[OK]"), org.Name, org.Slug, org.Role)
	fmt.Printf("   stored in %s\n", stored)

	if envOrg := strings.TrimSpace(os.Getenv("DIBBLA_ORG_ID")); envOrg != "" && !strings.EqualFold(envOrg, org.ID) {
		fmt.Printf("%s DIBBLA_ORG_ID is set to %s in this shell and takes precedence over what was just stored.\n",
			platform.Icon("⚠", "[!]"), envOrg)
	}
}

func runOrgClear(cmd *cobra.Command, args []string) {
	// Best-effort in both stores, mirroring logout: a host without a keyring
	// still has the file, and neither having anything to clear is success.
	if err := credential.DeleteOrg(); err != nil && !credential.IsKeyringUnavailable(err) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := credential.DeleteOrgFile(); err != nil {
		fmt.Printf("%s Warning: failed to update %s: %v\n",
			platform.Icon("⚠", "[!]"), credential.TokenFilePath(), err)
	}

	fmt.Printf("%s Organization cleared — using your account's default.\n", platform.Icon("✅", "[OK]"))

	if envOrg := strings.TrimSpace(os.Getenv("DIBBLA_ORG_ID")); envOrg != "" {
		fmt.Printf("%s DIBBLA_ORG_ID is still set to %s in this shell; unset it to fall back to the default.\n",
			platform.Icon("⚠", "[!]"), envOrg)
	}
}
