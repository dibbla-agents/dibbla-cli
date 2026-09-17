package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// The domains family (DIB-843): a customer's own hostname on a deployed app,
// on top of deploy-api's /deployments/{alias}/domains (DIB-842). The CLI's
// job is to hand back the exact DNS record the customer must create and to
// say, in one line, what Cloudflare is waiting for. Nothing here talks to a
// registrar.

var domainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "Serve an app on your own domain",
	Long: `Connect your own hostname (www.example.com) to a deployed app.

Adding a domain registers it at the platform's edge and prints the one DNS
record you create at your registrar: a CNAME from the hostname to the
platform's target. The certificate is issued automatically once the CNAME
resolves; 'verify' shows how far along it is.

Most registrars cannot put a CNAME on the bare domain (example.com): point
www at the platform and set up a redirect from the bare domain to www there.

Examples:
  dibbla domains add myapp www.example.com
  dibbla domains list myapp
  dibbla domains verify myapp www.example.com
  dibbla domains remove myapp www.example.com`,
}

var domainsListCmd = &cobra.Command{
	Use:   "list <alias>",
	Short: "List the app's domains with verification and certificate status",
	Args:  cobra.ExactArgs(1),
	Run:   runDomainsList,
}

var domainsAddCmd = &cobra.Command{
	Use:   "add <alias> <hostname>",
	Short: "Connect a domain to the app and print the DNS record to create",
	Args:  cobra.ExactArgs(2),
	Run:   runDomainsAdd,
}

var domainsVerifyCmd = &cobra.Command{
	Use:   "verify <alias> <hostname>",
	Short: "Fetch the domain's current status from the edge",
	Long: `Reads the hostname's live status from the edge provider and says what it
means: waiting for DNS, issuing the certificate, active, or an error with
its reason. Run it after creating the CNAME; DNS changes can take a few
minutes to propagate.`,
	Args: cobra.ExactArgs(2),
	Run:  runDomainsVerify,
}

var domainsRemoveCmd = &cobra.Command{
	Use:   "remove <alias> <hostname>",
	Short: "Disconnect a domain from the app",
	Long: `Removes the hostname from the app and from the edge. Your DNS record is
untouched; the hostname simply stops resolving to the app. Adding it again
starts a fresh verification.`,
	Args: cobra.ExactArgs(2),
	Run:  runDomainsRemove,
}

var (
	domainsListJSON   bool
	domainsAddJSON    bool
	domainsVerifyJSON bool
	domainsRemoveYes  bool
)

func init() {
	domainsCmd.AddCommand(domainsListCmd)
	domainsCmd.AddCommand(domainsAddCmd)
	domainsCmd.AddCommand(domainsVerifyCmd)
	domainsCmd.AddCommand(domainsRemoveCmd)

	domainsListCmd.Flags().BoolVar(&domainsListJSON, "json", false, "Print the raw API document")
	domainsAddCmd.Flags().BoolVar(&domainsAddJSON, "json", false, "Print the raw API document")
	domainsVerifyCmd.Flags().BoolVar(&domainsVerifyJSON, "json", false, "Print the domain's raw API document")
	domainsRemoveCmd.Flags().BoolVarP(&domainsRemoveYes, "yes", "y", false, "Skip confirmation prompt")
}

func runDomainsList(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runDomainsListCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], domainsListJSON))
}

func runDomainsAdd(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runDomainsAddCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], args[1], domainsAddJSON))
}

func runDomainsVerify(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runDomainsVerifyCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], args[1], domainsVerifyJSON))
}

func runDomainsRemove(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	alias, hostname := args[0], apps.NormalizeHostname(args[1])
	if !domainsRemoveYes {
		ok, err := askConfirm(fmt.Sprintf("Disconnect %s from '%s'? The hostname stops resolving to the app.", hostname, alias))
		if err != nil {
			os.Exit(refuseUnconfirmable(os.Stderr, fmt.Sprintf("removing domain %s", hostname)))
		}
		if !ok {
			fmt.Println("Cancelled.")
			os.Exit(0)
		}
	}
	os.Exit(runDomainsRemoveCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, alias, hostname))
}

// checkDomainArgs is the local refusal shared by every domains command:
// alias and hostname must look right before a request is issued. Returns
// the normalized hostname and 0, or the exit code to return.
func checkDomainArgs(stderr io.Writer, alias, hostname string) (string, int) {
	if !apps.AliasRe.MatchString(alias) {
		fmt.Fprintf(stderr, "%s alias %q does not match %s\n", platform.Icon("❌", "[X]"), alias, apps.AliasRe.String())
		return "", 5
	}
	if hostname == "" {
		return "", 0
	}
	h := apps.NormalizeHostname(hostname)
	if !apps.HostnameRe.MatchString(h) {
		fmt.Fprintf(stderr, "%s %q is not a hostname — expected something like www.example.com\n", platform.Icon("❌", "[X]"), hostname)
		return "", 5
	}
	return h, 0
}

// runDomainsListCore is the testable inner implementation of `domains list`.
func runDomainsListCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if _, code := checkDomainArgs(stderr, alias, ""); code != 0 {
		return code
	}
	list, raw, err := apps.ListDomains(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "domains list", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if len(list.Domains) == 0 {
		fmt.Fprintf(stdout, "%s No domains connected to '%s'.\n", platform.Icon("🌐", "[DNS]"), alias)
		fmt.Fprintf(stdout, "  Connect one with: dibbla domains add %s www.example.com\n", alias)
		return 0
	}
	fmt.Fprintf(stdout, "%s Domains on '%s' (%d):\n\n", platform.Icon("🌐", "[DNS]"), alias, len(list.Domains))
	fmt.Fprintf(stdout, "  %-40s %-10s %-20s %s\n", "HOSTNAME", "STATUS", "CERTIFICATE", "ACTIVE")
	for _, d := range list.Domains {
		active := "no"
		if d.Active {
			active = "yes"
		}
		fmt.Fprintf(stdout, "  %-40s %-10s %-20s %s\n", d.Hostname, orDash(d.Status), orDash(d.SSLStatus), active)
	}
	fmt.Fprintln(stdout)
	for _, d := range list.Domains {
		if !d.Active {
			fmt.Fprintf(stdout, "  %s: %s\n", d.Hostname, d.Verdict())
			for _, e := range d.Errors {
				fmt.Fprintf(stdout, "    - %s\n", e)
			}
		}
	}
	fmt.Fprintf(stdout, "\n  CNAME target: %s\n", list.CNAMETarget)
	return 0
}

// runDomainsAddCore is the testable inner implementation of `domains add`.
func runDomainsAddCore(stdout, stderr io.Writer, apiURL, apiToken, alias, hostname string, jsonOut bool) int {
	h, code := checkDomainArgs(stderr, alias, hostname)
	if code != 0 {
		return code
	}
	d, raw, err := apps.AddDomain(apiURL, apiToken, alias, h)
	if err != nil {
		return reportDomainError(stderr, "domains add", alias, h, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "%s %s is connected to '%s'.\n\n", platform.Icon("✅", "[OK]"), d.Hostname, alias)
	printDNSInstruction(stdout, d)
	fmt.Fprintf(stdout, "\n  Status: %s\n", d.Verdict())
	for _, e := range d.Errors {
		fmt.Fprintf(stdout, "    - %s\n", e)
	}
	fmt.Fprintf(stdout, "\n  Check progress with: dibbla domains verify %s %s\n", alias, d.Hostname)
	return 0
}

// printDNSInstruction is the record block `add` and `verify` both show: the
// exact CNAME, and the apex advice when the hostname has no www.
func printDNSInstruction(w io.Writer, d *apps.Domain) {
	fmt.Fprintf(w, "  Create this DNS record at your registrar:\n\n")
	fmt.Fprintf(w, "    Type:   %s\n", d.DNS.Type)
	fmt.Fprintf(w, "    Host:   %s\n", dnsHostLabel(d))
	fmt.Fprintf(w, "    Target: %s\n", d.DNS.Target)
	fmt.Fprintf(w, "\n    (%s)\n", d.DNS.Record)
	if d.IsApex {
		fmt.Fprintf(w, "\n  %s %s is a bare domain (apex).\n", platform.Icon("⚠️", "[!]"), d.Hostname)
		fmt.Fprintf(w, "  %s\n", d.ApexAdvice)
	} else {
		fmt.Fprintf(w, "\n  Bare domain: most registrars cannot put a CNAME on the bare domain;\n")
		fmt.Fprintf(w, "  set up an HTTP redirect from it to %s at your registrar instead.\n", d.Hostname)
	}
}

// dnsHostLabel is what a registrar's "Host"/"Name" field expects: @ for an
// apex (the server decides that, with its second-level-suffix heuristic),
// otherwise the leftmost label (www for www.example.com). Registrars differ
// — One.com and Loopia want the label, Cloudflare accepts the full name too
// — so the full record is printed alongside.
func dnsHostLabel(d *apps.Domain) string {
	if d.IsApex {
		return "@"
	}
	label, _, _ := strings.Cut(d.DNS.Name, ".")
	return label
}

// runDomainsVerifyCore is the testable inner implementation of `domains
// verify`. The listing refreshes every row from the edge on read, so a
// verify is the listing narrowed to one hostname.
func runDomainsVerifyCore(stdout, stderr io.Writer, apiURL, apiToken, alias, hostname string, jsonOut bool) int {
	h, code := checkDomainArgs(stderr, alias, hostname)
	if code != 0 {
		return code
	}
	list, _, err := apps.ListDomains(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "domains verify", alias, err)
	}
	d := list.FindDomain(h)
	if d == nil {
		fmt.Fprintf(stderr, "%s %s is not connected to '%s'.\n", platform.Icon("❌", "[X]"), h, alias)
		fmt.Fprintf(stderr, "  hint: dibbla domains add %s %s\n", alias, h)
		return 4
	}
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(d)
		return 0
	}
	if d.Active {
		fmt.Fprintf(stdout, "%s %s: %s\n", platform.Icon("✅", "[OK]"), d.Hostname, d.Verdict())
		fmt.Fprintf(stdout, "  https://%s\n", d.Hostname)
		return 0
	}
	fmt.Fprintf(stdout, "%s %s: %s\n", platform.Icon("⏳", "[..]"), d.Hostname, d.Verdict())
	fmt.Fprintf(stdout, "  Hostname status: %s\n", orDash(d.Status))
	fmt.Fprintf(stdout, "  Certificate:     %s\n", orDash(d.SSLStatus))
	for _, e := range d.Errors {
		fmt.Fprintf(stdout, "  - %s\n", e)
	}
	fmt.Fprintln(stdout)
	printDNSInstruction(stdout, d)
	return 0
}

// runDomainsRemoveCore is the testable inner implementation of `domains
// remove`, after the confirmation.
func runDomainsRemoveCore(stdout, stderr io.Writer, apiURL, apiToken, alias, hostname string) int {
	h, code := checkDomainArgs(stderr, alias, hostname)
	if code != 0 {
		return code
	}
	if err := apps.RemoveDomain(apiURL, apiToken, alias, h); err != nil {
		return reportDomainError(stderr, "domains remove", alias, h, err)
	}
	fmt.Fprintf(stdout, "%s %s is disconnected from '%s'.\n", platform.Icon("✅", "[OK]"), h, alias)
	fmt.Fprintf(stdout, "  Your DNS record is untouched; remove the CNAME at your registrar if you no longer need it.\n")
	return 0
}

// reportDomainError is reportAppError with the hostname in the context and
// the two domain-specific hints: a taken hostname is held by someone (the
// server never says who), and a domain that is not registered is spelled
// out rather than "check your aliases".
func reportDomainError(stderr io.Writer, verb, alias, hostname string, err error) int {
	if se, ok := err.(*apps.StatusError); ok {
		switch se.Code {
		case "DOMAIN_TAKEN":
			fmt.Fprintf(stderr, "%s %s is already connected to another app: %s\n", platform.Icon("❌", "[X]"), hostname, se.Message)
			fmt.Fprintln(stderr, "  hint: if it is yours, remove it from that app first (dibbla domains remove <alias> "+hostname+").")
			return se.ExitCode()
		case "DOMAIN_NOT_FOUND":
			fmt.Fprintf(stderr, "%s %s is not connected to '%s'.\n", platform.Icon("❌", "[X]"), hostname, alias)
			fmt.Fprintf(stderr, "  hint: dibbla domains list %s\n", alias)
			return se.ExitCode()
		case "DOMAIN_INVALID":
			fmt.Fprintf(stderr, "%s %s\n", platform.Icon("❌", "[X]"), se.Message)
			return se.ExitCode()
		case "DOMAINS_NOT_CONFIGURED":
			fmt.Fprintf(stderr, "%s Custom domains are not enabled on this installation: %s\n", platform.Icon("❌", "[X]"), se.Message)
			return se.ExitCode()
		}
	}
	return reportAppError(stderr, verb, alias, err)
}
