// Package notifycmd is `dibbla notifications` (DIB-1102): see, set up and
// test where Dibbla's alerts go without opening the console.
package notifycmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/notify"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

var notificationsCmd = &cobra.Command{
	Use:     "notifications",
	Aliases: []string{"notification", "notify"},
	Short:   "See, set up and test where Dibbla's alerts go",
	Long: `Dibbla alerts people when something needs attention: a failing check, a
maintenance finding or proposal, a new security finding, a failed deploy, a
pipeline that stopped. Owners and admins get their organization's app alerts
by email without setting anything up.

A personal subscription is yours: it is for one app you build and goes to your
own email (or your linked Slack account with --channel slack). Owners and
admins also manage the organization's subscriptions with --org-wide, which may
cover every app and name any address.

Event types come from the catalog ('dibbla notifications events'): a single
type such as application.check.failed, or a family such as
application.maintenance.*. --severity is the lowest severity delivered:
info, attention (default) or critical.

Examples:
  dibbla notifications list
  dibbla notifications events
  dibbla notifications add application.check.failed --app myapp
  dibbla notifications add application.maintenance.* --app myapp --severity info --test
  dibbla notifications add pipeline.run.* --org-wide --target ops@example.com
  dibbla notifications test 0b8e6a52
  dibbla notifications history --app myapp
  dibbla notifications remove 0b8e6a52`,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the subscriptions that reach you (and, for admins, the organization's)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runList(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, listApp, listJSON))
	},
}

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List the event types you can subscribe to",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runEvents(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, eventsJSON))
	},
}

var addCmd = &cobra.Command{
	Use:   "add <event-type>",
	Short: "Subscribe to an event type (personal for one app, or --org-wide)",
	Long: `Subscribe to an event type from the catalog.

Without --org-wide the subscription is personal: it needs --app, and it goes to
your own email address (or, with --channel slack, to your linked Slack
account). With --org-wide (owners and admins) it is the organization's: --app
is optional, --channel may be email, slack, teams, discourse or webhook, and an
email subscription names its --target.

Adding a subscription that already exists updates its severity and digest.
--test sends a test notification through it straight away.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runAdd(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, notify.AddRequest{
			EventType: args[0], App: addApp, MinSeverity: addSeverity, Channel: addChannel,
			Target: addTarget, Digest: addDigest, Organization: addOrgWide,
		}, addTest, addJSON))
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a subscription (yours, or the organization's if you are an admin)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runRemove(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], removeYes, confirm))
	},
}

var testCmd = &cobra.Command{
	Use:   "test <id>",
	Short: "Send a real test notification and show whether it arrived",
	Long: `Sends one real notification through the subscription now — whatever its
digest or severity — and prints what the channel answered: sent, or why not.
Exits 1 when it was not delivered.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runTest(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], testJSON))
	},
}

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show recent events and whether each reached anyone",
	Long: `Recent events and every delivery's status. Owners and admins see the whole
organization; everyone else sees what was delivered to them, or with --app
the events of an app they build.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := load()
		os.Exit(runHistory(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, historyApp, historyLimit, historyJSON))
	},
}

var (
	listApp, addApp, historyApp                   string
	addSeverity, addChannel, addTarget, addDigest string
	addOrgWide, addTest                           bool
	listJSON, eventsJSON, addJSON, testJSON       bool
	historyJSON, removeYes                        bool
	historyLimit                                  int
)

// Register adds `dibbla notifications` to root.
func Register(root *cobra.Command) {
	root.AddCommand(notificationsCmd)
}

func init() {
	notificationsCmd.AddCommand(listCmd, eventsCmd, addCmd, removeCmd, testCmd, historyCmd)

	listCmd.Flags().StringVar(&listApp, "app", "", "Only what applies to this app")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Print the raw API document")

	eventsCmd.Flags().BoolVar(&eventsJSON, "json", false, "Print the raw API document")

	addCmd.Flags().StringVar(&addApp, "app", "", "The app the subscription is for (required unless --org-wide)")
	addCmd.Flags().StringVar(&addSeverity, "severity", "", "Lowest severity delivered: info, attention (default) or critical")
	addCmd.Flags().StringVar(&addChannel, "channel", "", "email (default), slack; --org-wide also teams, discourse, webhook")
	addCmd.Flags().StringVar(&addTarget, "target", "", "Email address of an --org-wide email subscription")
	addCmd.Flags().StringVar(&addDigest, "digest", "", "immediate (default), hourly or daily")
	addCmd.Flags().BoolVar(&addOrgWide, "org-wide", false, "An organization subscription instead of a personal one (owners and admins)")
	addCmd.Flags().BoolVar(&addTest, "test", false, "Send a test notification through it straight away")
	addCmd.Flags().BoolVar(&addJSON, "json", false, "Print the raw API document")

	removeCmd.Flags().BoolVarP(&removeYes, "yes", "y", false, "Skip confirmation prompt")

	testCmd.Flags().BoolVar(&testJSON, "json", false, "Print the raw API document")

	historyCmd.Flags().StringVar(&historyApp, "app", "", "Only this app's events")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 20, "How many events, 1-100")
	historyCmd.Flags().BoolVar(&historyJSON, "json", false, "Print the raw API document")
}

func load() *config.Config {
	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Printf("%s Error: API token is required\n", platform.Icon("❌", "[X]"))
		fmt.Println("  Run: dibbla login (or set DIBBLA_API_TOKEN)")
		os.Exit(1)
	}
	return cfg
}

var (
	iconBell = func() string { return platform.Icon("🔔", "[i]") }
	iconOK   = func() string { return platform.Icon("✅", "[OK]") }
	iconFail = func() string { return platform.Icon("❌", "[X]") }
	iconWait = func() string { return platform.Icon("⏳", "[..]") }
)

// report prints an API refusal and returns its exit code.
func report(stderr io.Writer, what string, err error) int {
	var ne *notify.Error
	if errors.As(err, &ne) {
		fmt.Fprintf(stderr, "%s %s: %s\n", iconFail(), what, ne.Error())
		return ne.ExitCode()
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", iconFail(), what, err)
	return 1
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func describe(s notify.Subscription) string {
	what := s.EventType
	if s.Label != "" {
		what = s.Label + " (" + s.EventType + ")"
	}
	return what
}

func destination(channel, target string) string {
	if target == "" {
		return channel
	}
	return channel + " → " + target
}

func appOrEvery(app string) string {
	if app == "" {
		return "every app"
	}
	return app
}

func printSubscriptions(w io.Writer, title string, subs []notify.Subscription) {
	fmt.Fprintf(w, "%s (%d)\n", title, len(subs))
	for _, s := range subs {
		line := fmt.Sprintf("  %-8s  %s\n            %s · %s and up · %s", shortID(s.ID), describe(s), appOrEvery(s.App), s.MinSeverity, destination(s.Channel, s.Target))
		if s.Digest != "" && s.Digest != "immediate" {
			line += " · " + s.Digest + " digest"
		}
		if !s.Enabled {
			line += " · unsubscribed"
		}
		fmt.Fprintln(w, line)
	}
}

func runList(stdout, stderr io.Writer, apiURL, token, app string, jsonOut bool) int {
	list, raw, err := notify.List(apiURL, token, app)
	if err != nil {
		return report(stderr, "notifications list", err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	var mine, org []notify.Subscription
	for _, s := range list.Subscriptions {
		if s.Scope == "organization" {
			org = append(org, s)
		} else {
			mine = append(mine, s)
		}
	}
	if len(mine) == 0 && len(org) == 0 {
		fmt.Fprintf(stdout, "%s No notification subscriptions reach you", iconBell())
		if app != "" {
			fmt.Fprintf(stdout, " for %s", app)
		}
		fmt.Fprintln(stdout, ".")
		fmt.Fprintln(stdout, "  Add one: dibbla notifications add application.check.failed --app <app>")
		return 0
	}
	fmt.Fprintf(stdout, "%s ", iconBell())
	printSubscriptions(stdout, "Yours", mine)
	if list.CanManageOrganization {
		fmt.Fprintln(stdout)
		printSubscriptions(stdout, "The organization's", org)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  Test one: dibbla notifications test <id>   Remove: dibbla notifications remove <id>")
	return 0
}

func runEvents(stdout, stderr io.Writer, apiURL, token string, jsonOut bool) int {
	cat, raw, err := notify.GetCatalog(apiURL, token)
	if err != nil {
		return report(stderr, "notifications events", err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	for i, g := range cat.Groups {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintln(stdout, g.Label)
		fmt.Fprintf(stdout, "  %-44s %s\n", g.Family.EventType, g.Family.Label)
		for _, e := range g.Events {
			label := e.Label
			if e.Severity != "" {
				label += " [" + e.Severity + "]"
			}
			fmt.Fprintf(stdout, "  %-44s %s\n", e.EventType, label)
		}
	}
	return 0
}

func runAdd(stdout, stderr io.Writer, apiURL, token string, req notify.AddRequest, thenTest, jsonOut bool) int {
	req.EventType = strings.TrimSpace(req.EventType)
	if !req.Organization && req.App == "" {
		fmt.Fprintf(stderr, "%s A personal subscription is for one app: add --app <app>.\n", iconFail())
		fmt.Fprintln(stderr, "  Owners and admins can subscribe the organization with --org-wide.")
		return 5
	}
	res, raw, err := notify.Add(apiURL, token, req)
	if err != nil {
		return report(stderr, "notifications add", err)
	}
	if jsonOut && !thenTest {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	s := res.Subscription
	verb := "Subscribed"
	if !res.Created {
		verb = "Already subscribed; updated"
	}
	if !jsonOut {
		fmt.Fprintf(stdout, "%s %s: %s\n", iconOK(), verb, describe(s))
		fmt.Fprintf(stdout, "  %s · %s and up · %s  (id %s)\n", appOrEvery(s.App), s.MinSeverity, destination(s.Channel, s.Target), shortID(s.ID))
	}
	if !thenTest {
		fmt.Fprintf(stdout, "  Test it: dibbla notifications test %s\n", shortID(s.ID))
		return 0
	}
	return runTestID(stdout, stderr, apiURL, token, s.ID, jsonOut)
}

// resolveID accepts a full id or a unique prefix of one of the caller's
// visible subscriptions (the list prints eight characters).
func resolveID(apiURL, token, id string) (string, error) {
	id = strings.TrimSpace(id)
	if len(id) == 36 {
		return id, nil
	}
	if len(id) < 4 {
		return "", &notify.Error{Status: 400, Message: "give at least the first 4 characters of the id that 'dibbla notifications list' prints"}
	}
	list, _, err := notify.List(apiURL, token, "")
	if err != nil {
		return "", err
	}
	var hits []string
	for _, s := range list.Subscriptions {
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s.ID)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", &notify.Error{Status: 404, Message: "no subscription of yours starts with " + id + "; 'dibbla notifications list' shows them"}
	}
	sort.Strings(hits)
	return "", &notify.Error{Status: 400, Message: "more than one subscription starts with " + id + "; give more of the id"}
}

func runRemove(stdout, stderr io.Writer, apiURL, token, id string, yes bool, ask func(string) (bool, error)) int {
	full, err := resolveID(apiURL, token, id)
	if err != nil {
		return report(stderr, "notifications remove", err)
	}
	if !yes {
		ok, err := ask(fmt.Sprintf("Remove subscription %s? Its alerts stop reaching their destination.", shortID(full)))
		if err != nil {
			fmt.Fprintf(stderr, "%s removing a subscription needs confirmation, but stdin is not a terminal.\n", iconFail())
			fmt.Fprintln(stderr, "  Re-run with --yes to confirm non-interactively, or run it from a terminal.")
			return 5
		}
		if !ok {
			fmt.Fprintln(stdout, "Cancelled.")
			return 0
		}
	}
	if _, err := notify.Remove(apiURL, token, full); err != nil {
		return report(stderr, "notifications remove", err)
	}
	fmt.Fprintf(stdout, "%s Removed subscription %s.\n", iconOK(), shortID(full))
	return 0
}

func confirm(msg string) (bool, error) { return prompt.AskConfirmErr(msg) }

func runTest(stdout, stderr io.Writer, apiURL, token, id string, jsonOut bool) int {
	full, err := resolveID(apiURL, token, id)
	if err != nil {
		return report(stderr, "notifications test", err)
	}
	return runTestID(stdout, stderr, apiURL, token, full, jsonOut)
}

func runTestID(stdout, stderr io.Writer, apiURL, token, id string, jsonOut bool) int {
	res, raw, err := notify.Test(apiURL, token, id)
	if err != nil {
		return report(stderr, "notifications test", err)
	}
	d := res.Delivery
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
	} else if d.Status == "sent" {
		fmt.Fprintf(stdout, "%s Test notification sent: %s.\n", iconOK(), destination(d.Channel, d.Target))
		fmt.Fprintln(stdout, "  Look for “This is a test notification from Dibbla”.")
	} else {
		fmt.Fprintf(stderr, "%s Test notification not delivered (%s): %s\n", iconFail(), d.Status, destination(d.Channel, d.Target))
		if d.Reason != "" {
			fmt.Fprintf(stderr, "  %s\n", d.Reason)
		}
	}
	if d.Status != "sent" {
		return 1
	}
	return 0
}

func deliveryIcon(status string) string {
	switch status {
	case "sent":
		return iconOK()
	case "pending":
		return iconWait()
	}
	return iconFail()
}

func runHistory(stdout, stderr io.Writer, apiURL, token, app string, limit int, jsonOut bool) int {
	if limit < 1 || limit > 100 {
		fmt.Fprintf(stderr, "%s --limit must be 1-100\n", iconFail())
		return 5
	}
	h, raw, err := notify.GetHistory(apiURL, token, app, limit)
	if err != nil {
		return report(stderr, "notifications history", err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if len(h.Events) == 0 {
		fmt.Fprintf(stdout, "%s No recent events", iconBell())
		if app != "" {
			fmt.Fprintf(stdout, " for %s", app)
		}
		fmt.Fprintln(stdout, ".")
		return 0
	}
	for _, e := range h.Events {
		line := fmt.Sprintf("%s  [%s] %s", e.CreatedAt.Local().Format("2006-01-02 15:04"), e.Severity, e.Headline)
		if e.App != "" {
			line += " — " + e.App
		}
		fmt.Fprintln(stdout, line)
		if len(e.Deliveries) == 0 {
			fmt.Fprintln(stdout, "    (no delivery you can see)")
		}
		for _, d := range e.Deliveries {
			l := fmt.Sprintf("    %s %s: %s", deliveryIcon(d.Status), destination(d.Channel, d.Target), d.Status)
			if d.Reason != "" {
				l += " — " + d.Reason
			}
			fmt.Fprintln(stdout, l)
		}
	}
	if h.Scope != "organization" {
		fmt.Fprintln(stdout, "\n  You see your own deliveries; owners and admins see the whole organization's.")
	}
	return 0
}
