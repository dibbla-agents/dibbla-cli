package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
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

On a given server your API token is tied to your user rather than to one
organization, so switching needs no new login: the chosen organization is sent
with each request as X-Org-ID and the API verifies your membership before
honoring it.

That is true per server and false across servers. An organization id is issued
by, and only means anything on, the server that issued it — so the selection is
stored on the active context and lists the organizations on THAT server.
Switching context switches organization with it. See ` + "`dibbla context`" + `.

With no organization selected the API uses your account's default — the same
one the console opens on.

Precedence, highest first:
  --org <id>          just this invocation
  DIBBLA_ORG_ID       this shell
  dibbla org use ...  stored on the active context, until you change it
  (none)              your account's default organization on that server`,
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

Accepts a name, a slug, or an id. The selection is stored on the ACTIVE CONTEXT
— the server you are currently talking to — so it persists across invocations
and directories until you run "dibbla org clear" or log out, and switching
context switches organization with it.

It is per-context rather than machine-wide because an organization id is only
meaningful on the server that issued it. Sending one server's organization to
another is a wrong-org read or write, which the API answers with a 403 at best
and honors at worst.`,
	Args: cobra.ExactArgs(1),
	Run:  runOrgUse,
}

var orgClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Go back to your account's default organization on this context",
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
		rows = append(rows, []string{marker, o.Name, o.Slug, o.Role, o.Plan, o.ID})
	}
	output.PrintTable([]string{"", "NAME", "SLUG", "ROLE", "PLAN", "ID"}, rows)

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

	stored, err := storeOrgPin(cfg, org.ID, org.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
	cfg := config.Load()
	if _, err := storeOrgPin(cfg, "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Context != "" {
		fmt.Printf("%s Organization cleared on context %s — using your account's default there.\n",
			platform.Icon("✅", "[OK]"), cfg.Context)
	} else {
		fmt.Printf("%s Organization cleared — using your account's default.\n", platform.Icon("✅", "[OK]"))
	}

	if envOrg := strings.TrimSpace(os.Getenv("DIBBLA_ORG_ID")); envOrg != "" {
		fmt.Printf("%s DIBBLA_ORG_ID is still set to %s in this shell; unset it to fall back to the default.\n",
			platform.Icon("⚠", "[!]"), envOrg)
	}
}

// storeOrgPin writes the organization pin onto the active context and returns a
// human-readable description of where it landed. An empty id clears the pin.
//
// This is the heart of P-0011 Part C. Before named contexts the pin lived in a
// single machine-wide slot, which was correct while there was exactly one
// server. With per-context servers it stops being correct: switching from
// production to a customer instance would keep sending the previous server's
// X-Org-ID to a different API, because an organization id only means anything
// on the server that issued it.
//
// The legacy machine-wide slot is still written, but as a MIRROR of the active
// context rather than as the source of truth — see config.SyncLegacyMirror, and
// the reasoning in config.Migrate. That is what keeps a dibbla binary older
// than contexts pointed at the right organization.
//
// The fallback for a machine with no context at all (nothing stored, nothing
// migrated, so the user is running against the default endpoint with an env
// token) is the old behaviour: write the machine-wide slot directly. There is
// nowhere else to put it, and it is what the next command will read.
func storeOrgPin(cfg *config.Config, orgID, orgName string) (where string, err error) {
	if cfg.Context == "" {
		if orgID == "" {
			if derr := credential.DeleteOrg(); derr != nil && !credential.IsKeyringUnavailable(derr) {
				return "", derr
			}
			return "", credential.DeleteOrgFile()
		}
		if serr := credential.SetOrg(orgID, orgName); serr != nil {
			if !credential.IsKeyringUnavailable(serr) {
				return "", serr
			}
			if ferr := credential.SetOrgFile(orgID, orgName); ferr != nil {
				return "", ferr
			}
			return credential.TokenFilePath(), nil
		}
		return "keychain", nil
	}

	store, err := contextcfg.Load()
	if err != nil {
		return "", err
	}
	ctx, ok := store.Get(cfg.Context)
	if !ok {
		return "", fmt.Errorf("no such context %q", cfg.Context)
	}
	ctx.Org, ctx.OrgName = orgID, orgName
	store.Set(cfg.Context, ctx)
	if serr := store.Save(); serr != nil {
		return "", serr
	}
	// Keep the compatibility mirror in step, so an older binary does not keep
	// sending the organization that was just changed.
	config.SyncLegacyMirror()
	return fmt.Sprintf("context %s (%s)", cfg.Context, contextcfg.Path()), nil
}
