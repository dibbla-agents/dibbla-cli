package admincmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// `dibbla admin costs --org <org> [--app <app>]` prints one organization's
// month drilled down (DIB-1121): the AI gateway's
// /console/api/admin/costs/orgs/{org}, the same answer the console's
// drill-down page shows.

type appCostLine struct {
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	costUsage
	ViaDeploymentCalls int64    `json:"via_deployment_calls"`
	ActiveUsers        int64    `json:"active_users"`
	CostPerUserEUR     *float64 `json:"cost_per_user_eur"`
	PricePerUserEUR    *float64 `json:"price_per_user_eur"`
}

type costDrill struct {
	Month string `json:"month"`
	FX    struct {
		USDPerEUR *float64 `json:"usd_per_eur"`
	} `json:"fx"`
	Org         orgCosts      `json:"org"`
	Total       costUsage     `json:"total"`
	ActiveUsers int64         `json:"active_users"`
	Apps        []appCostLine `json:"apps"`
	Users       []struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		costUsage
	} `json:"users"`
	Purposes []struct {
		PrincipalClass string `json:"principal_class"`
		Segment        string `json:"segment"`
		costUsage
	} `json:"purposes"`
	Models []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		costUsage
	} `json:"models"`
	Days []struct {
		Day string `json:"day"`
		costUsage
	} `json:"days"`
}

// purposeNames are what a principal_class is for; another class is shown as
// recorded.
var purposeNames = map[string]string{
	"user":              "customer traffic",
	"application_check": "application checks",
	"maintenance_agent": "maintenance agent",
}

func appName(app, deployment string) string {
	if deployment != "" {
		return "deployment:" + deployment
	}
	if app == "" {
		return "(no app)"
	}
	return app
}

// runCostDrill drills org down; report is the month's admin/costs answer, in
// which org is looked up by slug or id.
func runCostDrill(c catalogClient, report []byte, month, org, app string, stdout, stderr io.Writer) int {
	var rep costReport
	if err := json.Unmarshal(report, &rep); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	id := ""
	for _, o := range rep.Orgs {
		if o.OrgID == org || strings.EqualFold(o.OrgSlug, org) {
			id = o.OrgID
			break
		}
	}
	if id == "" {
		fmt.Fprintf(stderr, "%s no organization %q has AI calls in %s (give its slug or id)\n", platform.Icon("❌", "[X]"), org, month)
		return 4
	}
	q := url.Values{"month": {month}}
	if d, ok := strings.CutPrefix(app, "deployment:"); ok {
		q.Set("deployment", d)
	} else if app != "" {
		q.Set("app", app)
	}
	resp, err := c.Get(costsPath + "/orgs/" + url.PathEscape(id) + "?" + q.Encode())
	if err != nil {
		return reportCostsError(stderr, err)
	}
	if costsFlags.json {
		_, _ = stdout.Write(resp.Body)
		if len(resp.Body) > 0 && resp.Body[len(resp.Body)-1] != '\n' {
			fmt.Fprintln(stdout)
		}
		return 0
	}
	var d costDrill
	if err := json.Unmarshal(resp.Body, &d); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	printCostDrill(stdout, d, app)
	return 0
}

func printCostDrill(stdout io.Writer, d costDrill, app string) {
	name := orgName(d.Org)
	if app != "" {
		name += " · " + app
	}
	fmt.Fprintf(stdout, "What %s cost Dibbla, %s (UTC)\n\n", name, d.Month)
	if d.Total.Requests == 0 {
		fmt.Fprintf(stdout, "No AI calls in %s.\n", d.Month)
		return
	}
	perUser := "—"
	if d.ActiveUsers > 0 && d.FX.USDPerEUR != nil {
		perUser = money(d.Total.CostUSD / *d.FX.USDPerEUR / float64(d.ActiveUsers), "€")
	}
	fmt.Fprintf(stdout, "Cost %s · customer pays %s · margin %s · %s calls · %d active users · cost per user %s\n",
		costCell(d.Total), money(d.Total.PriceEUR, "€"), marginCell(d.Total), count(d.Total.Requests), d.ActiveUsers, perUser)

	section := func(title, head string, rows func(w *tabwriter.Writer)) {
		fmt.Fprintf(stdout, "\n%s\n", title)
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, head)
		rows(w)
		_ = w.Flush()
	}
	const cols = "\tCALLS\tTOKENS IN\tTOKENS OUT\tCOST (USD)\tCUSTOMER PAYS (EUR)\tMARGIN (EUR)"
	cells := func(u costUsage, ops bool) string {
		pays := money(u.PriceEUR, "€")
		if ops || u.PricedCalls == 0 {
			pays = "—"
		}
		return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", count(u.Requests), count(u.PromptTokens), count(u.CompletionTokens), costCell(u), pays, marginCell(u))
	}

	if app == "" {
		section("BY APP", "APP"+cols+"\tUSERS\tCOST/USER\tPAYS/USER\tNOTES", func(w *tabwriter.Writer) {
			for _, a := range d.Apps {
				n := notes(a.costUsage)
				if a.ViaDeploymentCalls > 0 {
					n = strings.TrimPrefix(n+fmt.Sprintf(", %d by deployment", a.ViaDeploymentCalls), ", ")
				}
				users, cpu, ppu := "—", "—", "—"
				if a.ActiveUsers > 0 {
					users = count(a.ActiveUsers)
				}
				if a.CostPerUserEUR != nil {
					cpu = money(*a.CostPerUserEUR, "€")
				}
				if a.PricePerUserEUR != nil {
					ppu = money(*a.PricePerUserEUR, "€")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", appName(a.App, a.DeploymentID), cells(a.costUsage, false), users, cpu, ppu, n)
			}
		})
	}
	section("BY USER", "USER"+cols+"\tNOTES", func(w *tabwriter.Writer) {
		for _, u := range d.Users {
			who := u.Email
			if who == "" {
				who = u.UserID
			}
			if u.UserID == "" {
				who = "(machine calls)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", who, cells(u.costUsage, false), notes(u.costUsage))
		}
	})
	section("BY PURPOSE", "PURPOSE"+cols+"\tNOTES", func(w *tabwriter.Writer) {
		for _, p := range d.Purposes {
			n := purposeNames[p.PrincipalClass]
			if n == "" {
				n = p.PrincipalClass
			}
			if p.Segment == "ops" {
				n += " (ops)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", n, cells(p.costUsage, p.Segment == "ops"), notes(p.costUsage))
		}
	})
	section("BY MODEL", "MODEL"+cols+"\tNOTES", func(w *tabwriter.Writer) {
		for _, m := range d.Models {
			fmt.Fprintf(w, "%s/%s\t%s\t%s\n", m.Provider, m.Model, cells(m.costUsage, false), notes(m.costUsage))
		}
	})
	section("BY DAY", "DAY"+cols+"\tNOTES", func(w *tabwriter.Writer) {
		for _, x := range d.Days {
			fmt.Fprintf(w, "%s\t%s\t%s\n", x.Day, cells(x.costUsage, false), notes(x.costUsage))
		}
	})
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Every breakdown adds up to the total above. Ops: what Dibbla pays for itself (the Maintenance Agent). ≈: partly estimated at today's catalog price.")
}

func costCell(u costUsage) string {
	if u.CostedCalls+u.EstimatedCalls == 0 && u.OwnKeyCalls == 0 {
		return "—"
	}
	c := money(u.CostUSD, "$")
	if u.EstimatedCalls > 0 {
		c = "≈" + c
	}
	return c
}

func marginCell(u costUsage) string {
	if u.MarginEUR == nil || u.Requests == 0 {
		return "—"
	}
	m := *u.MarginEUR
	if math.Abs(m) < 0.00005 {
		m = 0
	}
	return money(m, "€")
}
