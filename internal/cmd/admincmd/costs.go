package admincmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// `dibbla admin costs` prints what every customer's AI calls cost Dibbla in a
// month (DIB-1120): the AI gateway's /console/api/admin/costs, the same answer
// the console's Customer costs page shows. Global admins only.

const costsPath = "/console/api/admin/costs"

type costUsage struct {
	Requests         int64    `json:"requests"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CostUSD          float64  `json:"cost_usd"`
	CostedCalls      int64    `json:"costed_calls"`
	EstimatedCalls   int64    `json:"estimated_calls"`
	UncostedCalls    int64    `json:"uncosted_calls"`
	PriceEUR         float64  `json:"price_eur"`
	PricedCalls      int64    `json:"priced_calls"`
	UnpricedCalls    int64    `json:"unpriced_calls"`
	OwnKeyCalls      int64    `json:"own_key_calls"`
	BackfilledCalls  int64    `json:"backfilled_calls"`
	MarginEUR        *float64 `json:"margin_eur"`
}

type orgCosts struct {
	OrgID    string    `json:"org_id"`
	OrgSlug  string    `json:"org_slug"`
	Ops      costUsage `json:"ops"`
	Customer costUsage `json:"customer"`
	Total    costUsage `json:"total"`
}

type costReport struct {
	Month string `json:"month"`
	FX    struct {
		USDPerEUR *float64 `json:"usd_per_eur"`
		Source    string   `json:"source"`
		RateDate  string   `json:"rate_date"`
	} `json:"fx"`
	Orgs   []orgCosts `json:"orgs"`
	Totals orgCosts   `json:"totals"`
}

var costsFlags struct {
	month string
	json  bool
	sort  string
	org   string
	app   string
}

var costsCmd = &cobra.Command{
	Use:   "costs",
	Short: "What every customer's AI calls cost Dibbla in a month (global admins)",
	Long: `What every organization's AI calls cost Dibbla in one calendar month (UTC):
calls, tokens, the purchase price (USD), what the customer pays (EUR) and the
margin, each organization split into ops (what Dibbla runs for it and pays
for itself under the Maintenance Agent) and its own traffic.

The same figures as the AI console's Customer costs page. Calls without a
recorded cost are estimated at today's catalog price ("estimated"); calls
that cannot be costed are counted ("N without cost"), never as zero; calls
on the customer's own provider key cost Dibbla nothing ("own key").

With --org, one organization's month drilled down: per app (machine calls
resolved to their app by deployment), with each app's active users and cost
per user, per user, per purpose (customer traffic, application checks, the
maintenance agent and any other recorded), per model and per day. Every
breakdown adds up to the organization's row. --app narrows the breakdowns to
one app ("deployment:<id>" for a deployment no call names an app for).

The cost view exists only where the installation turns it on (Dibbla's own);
elsewhere the command says so.

Requires a global admin login ('dibbla login').`,
	Example: `  dibbla admin costs
  dibbla admin costs --month 2026-10
  dibbla admin costs --month 2026-10 --sort margin
  dibbla admin costs --month 2026-10 --json
  dibbla admin costs --org acme
  dibbla admin costs --org acme --app shop --month 2026-10`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runCosts(newCatalogClient, os.Stdout, os.Stderr))
	},
}

func init() {
	f := costsCmd.Flags()
	f.StringVar(&costsFlags.month, "month", "", "Calendar month, YYYY-MM (default: the current month)")
	f.BoolVar(&costsFlags.json, "json", false, "Emit the gateway's JSON answer")
	f.StringVar(&costsFlags.sort, "sort", "cost", "Order: cost, price, margin, calls, tokens or org")
	f.StringVar(&costsFlags.org, "org", "", "Drill one organization down (its slug or id)")
	f.StringVar(&costsFlags.app, "app", "", "With --org: only this app (alias, or deployment:<id>)")
}

// notInstalled is what a 404 from the cost routes means: the installation
// does not run the cost view (ADMIN_COSTS_ENABLED off in the gateway).
const notInstalled = "the cost view is not available in this installation (it is turned on only in Dibbla's own installations)"

func reportCostsError(stderr io.Writer, err error) int {
	if apiErr, ok := err.(*apiclient.APIError); ok && apiErr.StatusCode == 404 {
		fmt.Fprintf(stderr, "%s %s\n", platform.Icon("❌", "[X]"), notInstalled)
		return apiclient.ExitCodeForStatus(404)
	}
	return reportErrorFor(stderr, err, "customer costs are for global admins only")
}

var monthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func runCosts(newClient func(io.Writer) (catalogClient, bool), stdout, stderr io.Writer) int {
	month := strings.TrimSpace(costsFlags.month)
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	if !monthPattern.MatchString(month) {
		fmt.Fprintf(stderr, "%s --month must be YYYY-MM, got %q\n", platform.Icon("❌", "[X]"), month)
		return 1
	}
	less, ok := costsOrder[costsFlags.sort]
	if !ok {
		fmt.Fprintf(stderr, "%s --sort must be cost, price, margin, calls, tokens or org\n", platform.Icon("❌", "[X]"))
		return 1
	}
	if costsFlags.app != "" && strings.TrimSpace(costsFlags.org) == "" {
		fmt.Fprintf(stderr, "%s --app needs --org\n", platform.Icon("❌", "[X]"))
		return 1
	}
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	resp, err := c.Get(costsPath + "?month=" + url.QueryEscape(month))
	if err != nil {
		return reportCostsError(stderr, err)
	}
	if org := strings.TrimSpace(costsFlags.org); org != "" {
		return runCostDrill(c, resp.Body, month, org, strings.TrimSpace(costsFlags.app), stdout, stderr)
	}
	if costsFlags.json {
		_, _ = stdout.Write(resp.Body)
		if len(resp.Body) > 0 && resp.Body[len(resp.Body)-1] != '\n' {
			fmt.Fprintln(stdout)
		}
		return 0
	}
	var rep costReport
	if err := json.Unmarshal(resp.Body, &rep); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	sort.SliceStable(rep.Orgs, func(i, j int) bool { return less(rep.Orgs[i], rep.Orgs[j]) })
	printCosts(stdout, rep)
	return 0
}

// costsOrder is each --sort: the largest first, the org name A–Z.
var costsOrder = map[string]func(a, b orgCosts) bool{
	"cost":  func(a, b orgCosts) bool { return a.Total.CostUSD > b.Total.CostUSD },
	"price": func(a, b orgCosts) bool { return a.Total.PriceEUR > b.Total.PriceEUR },
	"margin": func(a, b orgCosts) bool {
		return marginOrLowest(a.Total) > marginOrLowest(b.Total)
	},
	"calls": func(a, b orgCosts) bool { return a.Total.Requests > b.Total.Requests },
	"tokens": func(a, b orgCosts) bool {
		return a.Total.PromptTokens+a.Total.CompletionTokens > b.Total.PromptTokens+b.Total.CompletionTokens
	},
	"org": func(a, b orgCosts) bool { return strings.ToLower(orgName(a)) < strings.ToLower(orgName(b)) },
}

func marginOrLowest(u costUsage) float64 {
	if u.MarginEUR == nil {
		return math.Inf(-1)
	}
	return *u.MarginEUR
}

func orgName(o orgCosts) string {
	if o.OrgSlug != "" {
		return o.OrgSlug
	}
	return o.OrgID
}

func printCosts(stdout io.Writer, rep costReport) {
	fmt.Fprintf(stdout, "What customers cost Dibbla, %s (UTC)\n\n", rep.Month)
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ORGANIZATION\tPART\tCALLS\tTOKENS IN\tTOKENS OUT\tCOST (USD)\tCUSTOMER PAYS (EUR)\tMARGIN (EUR)\tNOTES")
	line := func(name, part string, u costUsage, ops bool) {
		pays := money(u.PriceEUR, "€")
		if ops {
			pays = "—"
		} else if u.PricedCalls == 0 {
			pays = "—"
		}
		cost := "—"
		if u.CostedCalls+u.EstimatedCalls > 0 || u.OwnKeyCalls > 0 {
			cost = money(u.CostUSD, "$")
			if u.EstimatedCalls > 0 {
				cost = "≈" + cost
			}
		}
		margin := "—"
		if u.MarginEUR != nil && u.Requests > 0 {
			// Below a hundredth of a cent the margin is rounding, not
			// money: the console shows it as €0.00 too.
			m := *u.MarginEUR
			if math.Abs(m) < 0.00005 {
				m = 0
			}
			margin = money(m, "€")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, part, count(u.Requests), count(u.PromptTokens), count(u.CompletionTokens),
			cost, pays, margin, notes(u))
	}
	if len(rep.Orgs) == 0 {
		_ = w.Flush()
		fmt.Fprintf(stdout, "No AI calls in %s.\n", rep.Month)
		return
	}
	for _, o := range rep.Orgs {
		line(orgName(o), "total", o.Total, false)
		if o.Ops.Requests > 0 {
			line("", "ops", o.Ops, true)
		}
		if o.Customer.Requests > 0 {
			line("", "own traffic", o.Customer, false)
		}
	}
	line(fmt.Sprintf("TOTAL (%d orgs)", len(rep.Orgs)), "total", rep.Totals.Total, false)
	line("", "ops", rep.Totals.Ops, true)
	line("", "own traffic", rep.Totals.Customer, false)
	_ = w.Flush()

	fmt.Fprintln(stdout)
	if r := rep.FX.USDPerEUR; r != nil {
		src := "ECB"
		if rep.FX.Source == "admin" {
			src = "admin override"
		}
		date := ""
		if len(rep.FX.RateDate) >= 10 {
			date = " of " + rep.FX.RateDate[:10]
		}
		fmt.Fprintf(stdout, "Margin at 1 EUR = %.4f USD (%s%s).\n", *r, src, date)
	} else {
		fmt.Fprintln(stdout, "No EUR rate is stored, so there is no margin.")
	}
	fmt.Fprintln(stdout, "Ops: what Dibbla runs for the organization and pays for itself (the Maintenance Agent). ≈: partly estimated at today's catalog price.")
}

// notes are the marks the console shows beside a figure.
func notes(u costUsage) string {
	var n []string
	if u.EstimatedCalls > 0 {
		n = append(n, fmt.Sprintf("%d estimated", u.EstimatedCalls))
	}
	if u.UncostedCalls > 0 {
		n = append(n, fmt.Sprintf("%d without cost", u.UncostedCalls))
	}
	if u.OwnKeyCalls > 0 {
		n = append(n, fmt.Sprintf("%d own key", u.OwnKeyCalls))
	}
	if u.UnpricedCalls > 0 {
		n = append(n, fmt.Sprintf("%d without a price", u.UnpricedCalls))
	}
	return strings.Join(n, ", ")
}

// money formats an amount as the console does: cents from 1 up, up to four
// decimals below, and "<0.0001" for a positive amount too small for that.
func money(v float64, symbol string) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	switch {
	case v > 0 && v < 0.00005:
		return sign + "<" + symbol + "0.0001"
	case v >= 1:
		return sign + symbol + thousands(fmt.Sprintf("%.2f", v))
	}
	s := strings.TrimRight(fmt.Sprintf("%.4f", v), "0")
	if i := strings.Index(s, "."); len(s)-i < 3 {
		s += strings.Repeat("0", 3-(len(s)-i))
	}
	return sign + symbol + s
}

func count(n int64) string {
	s := thousands(fmt.Sprintf("%d.", n))
	return strings.TrimSuffix(s, ".")
}

func thousands(s string) string {
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String() + "." + frac
}
