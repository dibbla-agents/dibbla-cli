package upgrade

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckoutAsksForTheReviewedOrgAndReturnsTheLink(t *testing.T) {
	var gotPath, gotAuth, gotOrg, gotReviewed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotOrg = r.Method+" "+r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Org-ID")
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReviewed = body["reviewed_organization_id"]
		_, _ = w.Write([]byte(`{"url":"https://checkout.stripe.com/c/pay/cs_test_abc"}`))
	}))
	defer srv.Close()

	u, reason, err := Checkout(srv.URL+"/", "tok", "org-1")
	if err != nil || reason != "" || u != "https://checkout.stripe.com/c/pay/cs_test_abc" {
		t.Fatalf("got (%q, %q, %v)", u, reason, err)
	}
	if gotPath != "POST /api/auth/v1/portal/billing/checkout" || gotAuth != "Bearer tok" || gotOrg != "org-1" || gotReviewed != "org-1" {
		t.Fatalf("request: %q auth=%q org=%q reviewed=%q", gotPath, gotAuth, gotOrg, gotReviewed)
	}
}

func TestCheckoutAnswers(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		reason  string
		errCode string
	}{
		{"already business", 409, `{"error":{"code":"ALREADY_SUBSCRIBED","message":"x"}}`, AlreadySubscribed, ""},
		{"enterprise", 409, `{"error":{"code":"ENTERPRISE_PLAN","message":"x"}}`, EnterprisePlan, ""},
		{"externally billed", 409, `{"error":{"code":"EXTERNALLY_BILLED","message":"x"}}`, ExternallyBilled, ""},
		{"member", 403, `{"error":{"code":"FORBIDDEN","message":"Insufficient role"}}`, RoleRequired, ""},
		{"billing off", 404, `404 page not found`, BillingDisabled, ""},
		{"org gone", 404, `{"error":{"code":"NOT_FOUND","message":"Organization not found"}}`, "", "NOT_FOUND"},
		{"login gone", 401, `{"error":{"code":"UNAUTHORIZED","message":"x"}}`, "", "UNAUTHORIZED"},
		{"org switched", 409, `{"error":{"code":"ORG_CHANGED","message":"x"}}`, "", "ORG_CHANGED"},
		{"stripe down", 502, `{"error":{"code":"STRIPE_ERROR","message":"Could not start checkout"}}`, "", "STRIPE_ERROR"},
		{"not https", 200, `{"url":"http://checkout.stripe.com/x"}`, "", "BAD_RESPONSE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			u, reason, err := Checkout(srv.URL, "tok", "org-1")
			if u != "" {
				t.Fatalf("no link expected, got %q", u)
			}
			if reason != c.reason {
				t.Fatalf("reason %q, want %q", reason, c.reason)
			}
			var ue *Error
			switch {
			case c.errCode == "" && err != nil:
				t.Fatalf("unexpected error %v", err)
			case c.errCode != "" && (!errors.As(err, &ue) || ue.Code != c.errCode):
				t.Fatalf("error %v, want code %s", err, c.errCode)
			}
		})
	}
}

func TestAnswersSayWhyWithoutALink(t *testing.T) {
	for _, reason := range []string{RoleRequired, AlreadySubscribed, EnterprisePlan, ExternallyBilled, BillingDisabled} {
		r := NotAvailable("org-1", "Acme", reason)
		if r.Status != StatusNotAvailable || r.CheckoutURL != "" || r.Reason != reason {
			t.Fatalf("%s: %+v", reason, r)
		}
		if strings.Contains(r.Message, "%") || strings.Contains(r.Message, "http") {
			t.Fatalf("%s: message %q", reason, r.Message)
		}
	}
	if m := NotAvailable("o", "", RoleRequired).Message; !strings.HasPrefix(m, "Only an owner or admin of this organization") {
		t.Fatalf("unnamed org: %q", m)
	}
	r := Ready("org-1", "Acme", "https://checkout.stripe.com/x")
	if r.Status != StatusReady || !strings.Contains(r.Message, "Business for Acme") || !strings.Contains(r.Message, "Nothing changes until the payment goes through") {
		t.Fatalf("ready: %+v", r)
	}
}

func TestCommandHint(t *testing.T) {
	review := "https://console.dibbla.com/org-settings/plan?upgrade=review"
	plan := "https://console.dibbla.com/org-settings/plan"
	if CommandHint("TRIAL_EXPIRED", review) == "" {
		t.Fatal("an ended trial names dibbla upgrade")
	}
	if CommandHint("PLAN_LIMIT_EXCEEDED", review) == "" {
		t.Fatal("a full trial names dibbla upgrade")
	}
	if CommandHint("PLAN_LIMIT_EXCEEDED", plan) != "" {
		t.Fatal("a full paid plan has nothing to upgrade")
	}
	if CommandHint("BUILD_FAILED", "") != "" {
		t.Fatal("other errors say nothing")
	}
}
