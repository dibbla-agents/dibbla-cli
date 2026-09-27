package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/auth"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/orgs"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/upgrade"
)

var (
	upgradeJSON bool
	upgradeOpen bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Get a payment link to upgrade your organization to Business",
	Long: `Print a Stripe Checkout link that upgrades the organization the CLI acts as
from its trial to Business — the same link the console's Plan tab and a
connected agent hand out.

Nothing changes until the payment goes through: the link only opens Stripe's
checkout, and it expires unused after 24 hours. Once paid, deploys work again
right away; running apps and data are untouched either way.

Only an owner or admin can upgrade. For anyone else, and for an organization
already on Business, on an Enterprise agreement or billed by Dibbla directly,
the command says so and prints no link.

Managing or cancelling an existing subscription is done in the console
(Org settings → Plan → Manage billing), not here.

Examples:
  dibbla upgrade            # print the link
  dibbla upgrade --open     # print it and open it in your browser
  dibbla upgrade --json     # {"status":"checkout_ready","checkout_url":"https://checkout.stripe.com/…",…}
  dibbla upgrade --org <id> # for another organization you belong to

Exit codes:
  0  answered: a link (status checkout_ready), or why there is none (not_available)
  3  not logged in
  1  the link could not be created (nothing has changed)`,
	Args: cobra.NoArgs,
	Run:  runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeJSON, "json", false, "Emit machine-readable JSON")
	upgradeCmd.Flags().BoolVar(&upgradeOpen, "open", false, "Also open the link in your browser")
}

// upgradeDeps is what runUpgradeWith reaches the network and the desktop
// through, so tests drive it without either.
type upgradeDeps struct {
	validate    func(apiURL, token, orgID string) (*apiclient.ValidateInfo, error)
	listOrgs    func(apiURL, token string) ([]orgs.Org, error)
	checkout    func(apiURL, token, orgID string) (string, string, error)
	openBrowser func(url string) error
}

var defaultUpgradeDeps = upgradeDeps{
	validate: apiclient.ValidateTokenDetailed,
	listOrgs: func(apiURL, token string) ([]orgs.Org, error) {
		return orgs.List(apiURL, token, false)
	},
	checkout:    upgrade.Checkout,
	openBrowser: auth.OpenBrowser,
}

func runUpgrade(cmd *cobra.Command, args []string) {
	os.Exit(runUpgradeWith(config.Load(), upgradeJSON, upgradeOpen, os.Stdout, os.Stderr, defaultUpgradeDeps))
}

func runUpgradeWith(cfg *config.Config, asJSON, open bool, stdout, stderr io.Writer, d upgradeDeps) int {
	if cfg.APIToken == "" {
		fmt.Fprintln(stderr, "Not logged in. Run 'dibbla login' first.")
		return 3
	}

	// Which org, and the caller's role in it, as the server sees them: the
	// pinned org when there is one, the account's default otherwise.
	info, err := d.validate(cfg.APIURL, cfg.APIToken, cfg.OrgID)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
			fmt.Fprintf(stderr, "Your login is not valid for this organization: %s\nRun 'dibbla login', or 'dibbla org use' to pick another organization.\n", strings.TrimSpace(apiErr.Message))
			return 3
		}
		fmt.Fprintf(stderr, "Could not reach Dibbla: %v\n", err)
		return 1
	}
	orgID := info.OrganizationID
	if orgID == "" {
		orgID = cfg.OrgID
	}
	if orgID == "" {
		fmt.Fprintln(stderr, "Could not tell which organization to upgrade. Pick one with 'dibbla org use <name>' and try again.")
		return 1
	}
	orgName := upgradeOrgName(cfg, info, orgID, d)

	var res upgrade.Result
	if !upgrade.IsAdmin(info.OrgRole) {
		res = upgrade.NotAvailable(orgID, orgName, upgrade.RoleRequired)
	} else {
		checkoutURL, reason, err := d.checkout(cfg.APIURL, cfg.APIToken, orgID)
		switch {
		case err != nil:
			var uerr *upgrade.Error
			if errors.As(err, &uerr) && uerr.Code == "UNAUTHORIZED" {
				fmt.Fprintln(stderr, err)
				return 3
			}
			if asJSON {
				out, _ := json.MarshalIndent(map[string]any{"error": err.Error(), "org_id": orgID}, "", "  ")
				fmt.Fprintln(stdout, string(out))
			} else {
				fmt.Fprintf(stderr, "%s %v\n", platform.Icon("❌", "[X]"), err)
			}
			return 1
		case reason != "":
			res = upgrade.NotAvailable(orgID, orgName, reason)
		default:
			res = upgrade.Ready(orgID, orgName, checkoutURL)
		}
	}

	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(stdout, string(out))
	} else {
		printUpgrade(stdout, res)
	}

	if open && res.CheckoutURL != "" {
		if err := d.openBrowser(res.CheckoutURL); err != nil {
			fmt.Fprintf(stderr, "Could not open a browser (%v) — open the link above instead.\n", err)
		} else if !asJSON {
			fmt.Fprintln(stdout, "Opened in your browser.")
		}
	}
	return 0
}

// printUpgrade draws the answer: the offer, the link bright on its own line,
// then what paying does. Without a link, only the explanation.
func printUpgrade(w io.Writer, res upgrade.Result) {
	if res.CheckoutURL == "" {
		fmt.Fprintf(w, "%s %s\n", platform.Icon("●", "[i]"), res.Message)
		return
	}
	offer, rest, _ := strings.Cut(res.Message, "\n\n")
	fmt.Fprintln(w, offer)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", res.CheckoutURL)
	if rest != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, rest)
	}
}

// upgradeOrgName is the org's display name for the message: the pinned name,
// else the org list's, else its slug. Cosmetic — any failure leaves it to
// "this organization".
func upgradeOrgName(cfg *config.Config, info *apiclient.ValidateInfo, orgID string, d upgradeDeps) string {
	if cfg.OrgName != "" && cfg.OrgID == orgID {
		return cfg.OrgName
	}
	if list, err := d.listOrgs(cfg.APIURL, cfg.APIToken); err == nil {
		for _, o := range list {
			if o.ID == orgID && o.Name != "" {
				return o.Name
			}
		}
	}
	return info.OrgSlug
}
