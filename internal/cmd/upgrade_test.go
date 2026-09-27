package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/orgs"
	"github.com/dibbla-agents/dibbla-cli/internal/upgrade"
)

const testCheckoutURL = "https://checkout.stripe.com/c/pay/cs_test_abc"

// fakeUpgrade answers as a server where the caller holds role in org-1, and
// counts checkout calls and browser opens.
type fakeUpgrade struct {
	role      string
	reason    string
	checkouts int
	opened    []string
}

func (f *fakeUpgrade) deps() upgradeDeps {
	return upgradeDeps{
		validate: func(_, _, _ string) (*apiclient.ValidateInfo, error) {
			return &apiclient.ValidateInfo{OrganizationID: "org-1", OrgRole: f.role, OrgSlug: "acme", OrgPlan: "trial"}, nil
		},
		listOrgs: func(_, _ string) ([]orgs.Org, error) {
			return []orgs.Org{{ID: "org-1", Name: "Acme AB"}}, nil
		},
		checkout: func(_, _, orgID string) (string, string, error) {
			f.checkouts++
			if f.reason != "" {
				return "", f.reason, nil
			}
			return testCheckoutURL, "", nil
		},
		openBrowser: func(u string) error { f.opened = append(f.opened, u); return nil },
	}
}

func runUpgradeTest(t *testing.T, f *fakeUpgrade, asJSON, open bool) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runUpgradeWith(&config.Config{APIURL: "https://api.example", APIToken: "tok"}, asJSON, open, &out, &errOut, f.deps())
	return out.String() + errOut.String(), code
}

func TestUpgradeOwnerGetsTheLink(t *testing.T) {
	f := &fakeUpgrade{role: "owner"}
	out, code := runUpgradeTest(t, f, false, false)
	if code != 0 || f.checkouts != 1 {
		t.Fatalf("code %d, checkouts %d", code, f.checkouts)
	}
	for _, want := range []string{"Business for Acme AB", "\n  " + testCheckoutURL + "\n", "Nothing changes until the payment goes through"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(f.opened) != 0 {
		t.Fatal("opened a browser without --open")
	}
}

func TestUpgradeOpenOpensTheLink(t *testing.T) {
	f := &fakeUpgrade{role: "admin"}
	out, code := runUpgradeTest(t, f, false, true)
	if code != 0 || len(f.opened) != 1 || f.opened[0] != testCheckoutURL || !strings.Contains(out, "Opened in your browser.") {
		t.Fatalf("code %d, opened %v, out:\n%s", code, f.opened, out)
	}
}

func TestUpgradeJSONCarriesTheLink(t *testing.T) {
	f := &fakeUpgrade{role: "owner"}
	out, code := runUpgradeTest(t, f, true, false)
	var res upgrade.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != 0 {
		t.Fatalf("code %d, not JSON (%v):\n%s", code, err, out)
	}
	if res.Status != "checkout_ready" || res.CheckoutURL != testCheckoutURL || res.OrgID != "org-1" {
		t.Fatalf("%+v", res)
	}
}

func TestUpgradeMemberIsToldWhoCanWithoutACall(t *testing.T) {
	for _, role := range []string{"member", "viewer", "developer"} {
		f := &fakeUpgrade{role: role}
		out, code := runUpgradeTest(t, f, true, true)
		var res upgrade.Result
		_ = json.Unmarshal([]byte(out), &res)
		if code != 0 || f.checkouts != 0 || len(f.opened) != 0 {
			t.Fatalf("%s: code %d, checkouts %d, opened %v", role, code, f.checkouts, f.opened)
		}
		if res.Status != "not_available" || res.Reason != "ROLE_REQUIRED" || res.CheckoutURL != "" ||
			!strings.Contains(res.Message, "Only an owner or admin of Acme AB") {
			t.Fatalf("%s: %+v", role, res)
		}
	}
}

func TestUpgradeServerRefusalIsAnAnswer(t *testing.T) {
	for _, reason := range []string{"ALREADY_SUBSCRIBED", "ENTERPRISE_PLAN", "EXTERNALLY_BILLED"} {
		f := &fakeUpgrade{role: "owner", reason: reason}
		out, code := runUpgradeTest(t, f, false, true)
		if code != 0 || len(f.opened) != 0 || strings.Contains(out, "https://") || !strings.Contains(out, "Acme AB") {
			t.Fatalf("%s: code %d, opened %v, out:\n%s", reason, code, f.opened, out)
		}
	}
}

func TestUpgradeNeedsALogin(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runUpgradeWith(&config.Config{APIURL: "https://api.example"}, false, false, &out, &errOut, (&fakeUpgrade{}).deps()); code != 3 {
		t.Fatalf("code %d", code)
	}
}
