package admincmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// DIB-1557: hosting in `dibbla admin costs`, and the monthly cost it divides.

const octoberWithHosting = `{
  "month": "2026-10",
  "fx": {"usd_per_eur": 1.25},
  "orgs": [
    {"org_id": "a", "org_slug": "acme",
     "ops": {"requests": 0}, "customer": {"requests": 4, "cost_usd": 2.5, "costed_calls": 4, "price_eur": 2.2, "priced_calls": 4, "margin_eur": 0.2},
     "total": {"requests": 4, "cost_usd": 2.5, "costed_calls": 4, "price_eur": 2.2, "priced_calls": 4, "margin_eur": 0.2},
     "hosting": {"eur": 75, "cpu_millicores": 2000, "memory_bytes": 8589934592}, "total_cost_eur": 77, "total_margin_eur": -74.8},
    {"org_id": "c", "org_slug": "bravo",
     "ops": {"requests": 0}, "customer": {"requests": 0}, "total": {"requests": 0, "margin_eur": 0},
     "hosting": {"eur": 37.5}, "total_cost_eur": 37.5, "total_margin_eur": -37.5}
  ],
  "totals": {"ops": {"requests": 0}, "customer": {"requests": 4}, "total": {"requests": 4, "cost_usd": 2.5, "costed_calls": 4, "price_eur": 2.2, "priced_calls": 4, "margin_eur": 0.2},
     "hosting": {"eur": 112.5}, "total_cost_eur": 114.5, "total_margin_eur": -112.3},
  "hosting_summary": {"status": "ok", "estimated": true, "days": 30, "days_in_month": 30, "cost_eur": 300, "platform": {"eur": 37.5}, "unused_eur": 150, "unallocated_eur": 0}
}`

func TestCostsShowsHostingAndTheTotal(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.month = "2026-10"
	f := &fakeCosts{body: []byte(octoberWithHosting)}
	var out, errb bytes.Buffer
	if code := runCosts(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"HOSTING ≈ (EUR)", "TOTAL COST (EUR)", "≈€75.00", "≈€77.00", "-€74.80", "bravo", "≈€37.50",
		"Dibbla platform", "Unused capacity", "≈€150.00", "Cluster (30 of 30 days)", "≈€300.00", "estimate"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	costsFlags.sort = "hosting"
	out.Reset()
	runCosts(f.factory(), &out, &errb)
	if strings.Index(out.String(), "acme") > strings.Index(out.String(), "bravo") {
		t.Errorf("--sort hosting: acme (€75) is not first:\n%s", out.String())
	}
}

func TestCostsSaysWhenNoHostingCostIsEntered(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.month = "2026-10"
	body := strings.Replace(october, `"orgs":`, `"hosting_summary": {"status": "no_cost"}, "orgs":`, 1)
	f := &fakeCosts{body: []byte(body)}
	var out, errb bytes.Buffer
	runCosts(f.factory(), &out, &errb)
	if strings.Contains(out.String(), "HOSTING") || !strings.Contains(out.String(), "dibbla admin hosting-cost set") {
		t.Errorf("no cost:\n%s", out.String())
	}
}

type fakeHosting struct {
	fakeCosts
	put     map[string]any
	putPath string
	delErr  error
}

func (f *fakeHosting) Put(path string, body interface{}) (*apiclient.Response, error) {
	f.putPath = path
	f.put = body.(map[string]any)
	b, _ := json.Marshal(map[string]any{"valid_from": "2026-10-01", "compute_eur": f.put["compute_eur"], "storage_eur": f.put["storage_eur"]})
	return &apiclient.Response{StatusCode: 200, Body: b}, nil
}
func (f *fakeHosting) Delete(string) (*apiclient.Response, error) {
	return &apiclient.Response{StatusCode: 204}, f.delErr
}

func TestHostingCostSetListDelete(t *testing.T) {
	defer func() {
		hostingCostFlags.from, hostingCostFlags.nodes, hostingCostFlags.storage, hostingCostFlags.note = "", -1, 0, ""
	}()
	f := &fakeHosting{}
	newClient := func(io.Writer) (catalogClient, bool) { return f, true }

	var out, errb bytes.Buffer
	hostingCostFlags.from, hostingCostFlags.nodes = "2026-10-1", 300
	if code := runHostingCostSet(newClient, &out, &errb); code != 1 {
		t.Errorf("bad date: exit %d", code)
	}
	hostingCostFlags.from, hostingCostFlags.nodes = "2026-10-01", -1
	if code := runHostingCostSet(newClient, &out, &errb); code != 1 {
		t.Errorf("no --nodes: exit %d", code)
	}
	hostingCostFlags.nodes, hostingCostFlags.storage, hostingCostFlags.note = 372, 31, "3 nodes"
	if code := runHostingCostSet(newClient, &out, &errb); code != 0 {
		t.Fatalf("set: exit %d %s", code, errb.String())
	}
	if f.putPath != hostingCostsPath+"/2026-10-01" || f.put["compute_eur"] != 372.0 || f.put["storage_eur"] != 31.0 || f.put["note"] != "3 nodes" {
		t.Errorf("PUT %s %v", f.putPath, f.put)
	}
	if !strings.Contains(out.String(), "€372.00") {
		t.Errorf("set said: %s", out.String())
	}

	f.body = []byte(`{"costs":[{"valid_from":"2026-10-01","compute_eur":372,"storage_eur":31,"note":"3 nodes","updated_by":"a@dibbla.com"}],"in_force":"2026-10-01"}`)
	out.Reset()
	if code := runHostingCostList(newClient, &out, &errb); code != 0 || !strings.Contains(out.String(), "in force") || !strings.Contains(out.String(), "€31.00") {
		t.Errorf("list: %d %s", code, out.String())
	}

	f.delErr = &apiclient.APIError{StatusCode: 404, Message: `{"error":{"code":"NOT_FOUND"}}`}
	errb.Reset()
	if code := runHostingCostDelete(newClient, &out, &errb); code != 4 || !strings.Contains(errb.String(), "no monthly cost") {
		t.Errorf("delete missing: %d %s", code, errb.String())
	}
	f.delErr = &apiclient.APIError{StatusCode: 404, Message: "404 page not found"}
	errb.Reset()
	runHostingCostDelete(newClient, &out, &errb)
	if !strings.Contains(errb.String(), "not available in this installation") {
		t.Errorf("view off: %s", errb.String())
	}
}
