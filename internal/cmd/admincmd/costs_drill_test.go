package admincmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// DIB-1121: `dibbla admin costs --org <org> [--app <app>]` prints the
// gateway's drill-down of one organization's month.

type fakeDrill struct {
	month, drill []byte
	status       int
	paths        []string
}

func (f *fakeDrill) Get(path string) (*apiclient.Response, error) {
	f.paths = append(f.paths, path)
	if f.status != 0 {
		return nil, &apiclient.APIError{StatusCode: f.status, Message: "404 page not found"}
	}
	if strings.Contains(path, "/orgs/") {
		return &apiclient.Response{StatusCode: 200, Body: f.drill}, nil
	}
	return &apiclient.Response{StatusCode: 200, Body: f.month}, nil
}
func (f *fakeDrill) Put(string, interface{}) (*apiclient.Response, error) { panic("no put") }
func (f *fakeDrill) Delete(string) (*apiclient.Response, error)           { panic("no delete") }
func (f *fakeDrill) factory() func(io.Writer) (catalogClient, bool) {
	return func(io.Writer) (catalogClient, bool) { return f, true }
}

const acmeDrill = `{
  "month": "2026-10",
  "fx": {"usd_per_eur": 1.25},
  "org": {"org_id": "a", "org_slug": "acme"},
  "app": null,
  "total": {"requests": 7, "prompt_tokens": 700, "completion_tokens": 70, "cost_usd": 3, "costed_calls": 7, "price_eur": 1.2, "priced_calls": 3, "margin_eur": -1.2},
  "active_users": 2,
  "apps": [
    {"app": "shop", "requests": 6, "cost_usd": 2.5, "costed_calls": 6, "price_eur": 1.2, "priced_calls": 3, "margin_eur": -0.8,
     "via_deployment_calls": 1, "active_users": 2, "cost_per_user_eur": 1, "price_per_user_eur": 0.6},
    {"app": "", "deployment_id": "gen-old", "requests": 1, "cost_usd": 0.5, "costed_calls": 1, "margin_eur": -0.4,
     "via_deployment_calls": 0, "active_users": 0, "cost_per_user_eur": null, "price_per_user_eur": null}
  ],
  "users": [{"user_id": "u1", "email": "u1@example.com", "requests": 6, "cost_usd": 2.5, "costed_calls": 6}, {"user_id": "", "requests": 1, "cost_usd": 0.5, "costed_calls": 1}],
  "purposes": [{"principal_class": "user", "segment": "customer", "requests": 6, "cost_usd": 2.5, "costed_calls": 6},
               {"principal_class": "ops_triage", "segment": "ops", "requests": 1, "cost_usd": 0.5, "costed_calls": 1}],
  "models": [{"provider": "anthropic", "model": "claude-sonnet-5", "requests": 7, "cost_usd": 3, "costed_calls": 7}],
  "days": [{"day": "2026-10-02", "requests": 6, "cost_usd": 2.5, "costed_calls": 6}, {"day": "2026-10-05", "requests": 1, "cost_usd": 0.5, "costed_calls": 1}]
}`

func TestCostsDrillsAnOrgDown(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.month, costsFlags.org = "2026-10", "ACME"
	f := &fakeDrill{month: []byte(october), drill: []byte(acmeDrill)}
	var out, errb bytes.Buffer
	if code := runCosts(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(f.paths) != 2 || f.paths[1] != "/console/api/admin/costs/orgs/a?month=2026-10" {
		t.Errorf("asked %v", f.paths)
	}
	s := out.String()
	for _, want := range []string{"What acme cost Dibbla, 2026-10", "2 active users", "cost per user €1.20",
		"BY APP", "shop", "€1.00", "€0.60", "1 by deployment", "deployment:gen-old",
		"BY USER", "u1@example.com", "(machine calls)", "BY PURPOSE", "customer traffic", "ops_triage (ops)",
		"BY MODEL", "anthropic/claude-sonnet-5", "BY DAY", "2026-10-05"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestCostsDrillOneApp(t *testing.T) {
	defer resetCostsFlags()
	for app, want := range map[string]string{"shop": "app=shop", "deployment:gen-old": "deployment=gen-old"} {
		resetCostsFlags()
		costsFlags.month, costsFlags.org, costsFlags.app = "2026-10", "a", app
		f := &fakeDrill{month: []byte(october), drill: []byte(acmeDrill)}
		var out, errb bytes.Buffer
		if code := runCosts(f.factory(), &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(f.paths[1], want) || strings.Contains(out.String(), "BY APP") {
			t.Errorf("--app %s: asked %s\n%s", app, f.paths[1], out.String())
		}
	}
}

func TestCostsDrillRefusals(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.app = "shop"
	var out, errb bytes.Buffer
	if code := runCosts((&fakeDrill{}).factory(), &out, &errb); code != 1 || !strings.Contains(errb.String(), "--app needs --org") {
		t.Errorf("--app alone: %d %q", code, errb.String())
	}
	resetCostsFlags()
	costsFlags.month, costsFlags.org = "2026-10", "nobody"
	errb.Reset()
	if code := runCosts((&fakeDrill{month: []byte(october)}).factory(), &out, &errb); code != 4 || !strings.Contains(errb.String(), `"nobody"`) {
		t.Errorf("unknown org: %d %q", code, errb.String())
	}
}

// Where the installation does not run the cost view, the routes are 404:
// the command says the function does not exist here, with or without --org.
func TestCostsNotInThisInstallation(t *testing.T) {
	defer resetCostsFlags()
	for _, org := range []string{"", "acme"} {
		resetCostsFlags()
		costsFlags.org = org
		var out, errb bytes.Buffer
		code := runCosts((&fakeDrill{status: 404}).factory(), &out, &errb)
		if code != 4 || !strings.Contains(errb.String(), "not available in this installation") {
			t.Errorf("--org %q: exit %d, %q", org, code, errb.String())
		}
	}
}
