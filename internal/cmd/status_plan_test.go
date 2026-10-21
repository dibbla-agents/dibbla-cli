package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Plan: line names the plan as the console does; the legacy "trial" an
// older server may still send reads Hobby (DIB-1519).
func TestStatusPlanLabel(t *testing.T) {
	for plan, want := range map[string]string{
		"hobby": "Hobby", "trial": "Hobby", "standard": "Business", "enterprise": "Enterprise", "gold": "gold",
	} {
		if got := planLabel(plan); got != want {
			t.Errorf("planLabel(%q) = %q, want %q", plan, got, want)
		}
	}
	if normalizePlan("trial") != "hobby" || normalizePlan("standard") != "standard" {
		t.Error("normalizePlan must read trial as hobby and leave the rest")
	}
}

// A Hobby org names dibbla upgrade under Plan: (DIB-1047); other plans get
// nothing.
func TestStatusUpgradeLines(t *testing.T) {
	hobby := statusReport{Plan: "hobby", UpgradeCommand: upgradeCommand}
	if got := hobby.upgradeLines(); len(got) != 1 || got[0] != "upgrade: run dibbla upgrade to move to Business" {
		t.Fatalf("hobby: %q", got)
	}
	if got := (&statusReport{Plan: "standard"}).upgradeLines(); got != nil {
		t.Fatalf("business: %q", got)
	}
}

// Against an older server that still says trial and sends the trial clock,
// status reports Hobby, keeps the upgrade link and command, and carries none
// of the trial fields.
func TestStatusReadsAnOlderServersTrialAsHobby(t *testing.T) {
	const link = "https://console.dibbla.com/org-settings/plan?upgrade=review"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/v1/tokens/validate":
			_, _ = w.Write([]byte(`{"org_plan":"trial","org_trial_ends_at":"2020-01-01T00:00:00Z"}`))
		case "/api/deploy/plan":
			_, _ = w.Write([]byte(`{"plan":"trial","trial_ends_at":"2020-01-01T00:00:00Z","days_left":0,"trial_ended":true,"upgrade_url":"` + link + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("DIBBLA_API_URL", srv.URL)
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "")

	r := buildStatusReport(false)
	if !r.LoggedIn || r.Plan != "hobby" || r.UpgradeURL != link || r.UpgradeCommand != upgradeCommand {
		t.Fatalf("report: %+v", r)
	}
	out, _ := json.Marshal(r)
	if strings.Contains(string(out), "trial") {
		t.Errorf("JSON carries trial fields: %s", out)
	}
}
