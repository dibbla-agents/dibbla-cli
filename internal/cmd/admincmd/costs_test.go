package admincmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// DIB-1120: `dibbla admin costs` prints the gateway's admin/costs answer.

type fakeCosts struct {
	body   []byte
	status int
	paths  []string
}

func (f *fakeCosts) Get(path string) (*apiclient.Response, error) {
	f.paths = append(f.paths, path)
	if f.status != 0 {
		return nil, &apiclient.APIError{StatusCode: f.status, Message: `{"error":{"code":"FORBIDDEN","message":"global admin required"}}`}
	}
	return &apiclient.Response{StatusCode: 200, Body: f.body}, nil
}
func (f *fakeCosts) Put(string, interface{}) (*apiclient.Response, error) { panic("no put") }
func (f *fakeCosts) Delete(string) (*apiclient.Response, error)           { panic("no delete") }

func (f *fakeCosts) factory() func(io.Writer) (catalogClient, bool) {
	return func(io.Writer) (catalogClient, bool) { return f, true }
}

const october = `{
  "month": "2026-10",
  "fx": {"usd_per_eur": 1.25, "source": "ecb", "rate_date": "2026-10-20T00:00:00Z"},
  "orgs": [
    {"org_id": "b", "org_slug": "byok",
     "ops": {"requests": 0},
     "customer": {"requests": 4, "prompt_tokens": 1000, "completion_tokens": 100, "cost_usd": 0.06, "estimated_calls": 1, "uncosted_calls": 1, "own_key_calls": 1, "price_eur": 0.05, "priced_calls": 1, "margin_eur": 0.002},
     "total": {"requests": 4, "prompt_tokens": 1000, "completion_tokens": 100, "cost_usd": 0.06, "estimated_calls": 1, "uncosted_calls": 1, "own_key_calls": 1, "price_eur": 0.05, "priced_calls": 1, "margin_eur": 0.002}},
    {"org_id": "a", "org_slug": "acme",
     "ops": {"requests": 1, "prompt_tokens": 400000, "completion_tokens": 40000, "cost_usd": 1.8, "costed_calls": 1, "margin_eur": -1.44},
     "customer": {"requests": 3, "prompt_tokens": 300000, "completion_tokens": 30000, "cost_usd": 0.75, "costed_calls": 2, "price_eur": 0.86, "priced_calls": 2, "unpriced_calls": 1, "margin_eur": 0.26},
     "total": {"requests": 4, "prompt_tokens": 700000, "completion_tokens": 70000, "cost_usd": 2.55, "costed_calls": 3, "price_eur": 0.86, "priced_calls": 2, "unpriced_calls": 1, "margin_eur": -1.18}}
  ],
  "totals": {"org_id": "", "org_slug": "total",
     "ops": {"requests": 1, "cost_usd": 1.8, "costed_calls": 1, "margin_eur": -1.44},
     "customer": {"requests": 7, "cost_usd": 0.81, "costed_calls": 2, "estimated_calls": 1, "price_eur": 0.91, "priced_calls": 3, "margin_eur": 0.262},
     "total": {"requests": 8, "prompt_tokens": 701000, "completion_tokens": 70100, "cost_usd": 2.61, "costed_calls": 3, "estimated_calls": 1, "uncosted_calls": 1, "own_key_calls": 1, "price_eur": 0.91, "priced_calls": 3, "unpriced_calls": 1, "margin_eur": -1.178}}
}`

func resetCostsFlags() { costsFlags.month, costsFlags.json, costsFlags.sort = "", false, "cost" }

func TestCostsPrintsTheMonthPerOrgWithItsSplit(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.month = "2026-10"
	f := &fakeCosts{body: []byte(october)}
	var out, errb bytes.Buffer
	if code := runCosts(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if f.paths[0] != "/console/api/admin/costs?month=2026-10" {
		t.Errorf("asked %s", f.paths[0])
	}
	s := out.String()
	lines := strings.Split(s, "\n")
	idx := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		t.Fatalf("no line with %q in:\n%s", sub, s)
		return -1
	}
	// Most expensive first, each org with its ops and own traffic.
	if idx("acme") > idx("byok") {
		t.Errorf("acme (the most expensive) is not first:\n%s", s)
	}
	for _, want := range []string{"$2.55", "€0.86", "-€1.18", "$1.80", "1 without a price",
		"≈$0.06", "1 estimated", "1 without cost", "1 own key", "TOTAL (2 orgs)", "$2.61",
		"1 EUR = 1.2500 USD (ECB of 2026-10-20)"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if !strings.Contains(lines[idx("$1.80")], "ops") {
		t.Errorf("the ops line is not marked ops: %q", lines[idx("$1.80")])
	}
}

func TestCostsSortAndJSON(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	costsFlags.sort = "margin"
	f := &fakeCosts{body: []byte(october)}
	var out, errb bytes.Buffer
	if code := runCosts(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if s := out.String(); strings.Index(s, "byok") > strings.Index(s, "acme") {
		t.Errorf("--sort margin: byok (positive margin) is not first:\n%s", s)
	}

	resetCostsFlags()
	costsFlags.json = true
	out.Reset()
	if code := runCosts(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var back map[string]any
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || back["month"] != "2026-10" {
		t.Errorf("--json is not the gateway's answer: %v %s", err, out.String())
	}
}

func TestCostsRefusals(t *testing.T) {
	defer resetCostsFlags()
	resetCostsFlags()
	f := &fakeCosts{status: 403}
	var out, errb bytes.Buffer
	if code := runCosts(f.factory(), &out, &errb); code == 0 || !strings.Contains(errb.String(), "global admins only") {
		t.Errorf("403: exit %d, %q", code, errb.String())
	}
	for _, bad := range []string{"2026-13", "oct", "2026-10-01"} {
		resetCostsFlags()
		costsFlags.month = bad
		errb.Reset()
		if code := runCosts(f.factory(), &out, &errb); code != 1 || !strings.Contains(errb.String(), "YYYY-MM") {
			t.Errorf("--month %s: exit %d, %q", bad, code, errb.String())
		}
	}
	resetCostsFlags()
	costsFlags.sort = "nope"
	if code := runCosts(f.factory(), &out, &errb); code != 1 {
		t.Errorf("--sort nope: exit %d", code)
	}
}

func TestMoney(t *testing.T) {
	for v, want := range map[float64]string{2.5: "$2.50", 1234.5: "$1,234.50", 0.0123: "$0.0123", 0.5: "$0.50", 0.00001: "<$0.0001", -1.18: "-$1.18", 0: "$0.00"} {
		if got := money(v, "$"); got != want {
			t.Errorf("money(%v) = %q, want %q", v, got, want)
		}
	}
}
