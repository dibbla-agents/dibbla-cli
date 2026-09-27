package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

const statusUpgrade = "https://console.dibbla.com/org-settings/plan?upgrade=review"

func TestTrialClockIsTheConsolesRule(t *testing.T) {
	end := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	endsAt := end.Format(time.RFC3339)
	for _, c := range []struct {
		name  string
		now   time.Time
		days  int
		ended bool
	}{
		{"five days and a bit", end.Add(-5*24*time.Hour - time.Minute), 6, false},
		{"an hour", end.Add(-time.Hour), 1, false},
		{"at the end", end, 0, true},
		{"three days past", end.Add(3 * 24 * time.Hour), 0, true},
	} {
		days, ended, ok := trialClock(endsAt, c.now)
		if !ok || days != c.days || ended != c.ended {
			t.Errorf("%s: got (%d, %v, %v)", c.name, days, ended, ok)
		}
	}
	if _, _, ok := trialClock("soon", end); ok {
		t.Error("an unreadable end was counted")
	}
}

func intp(n int) *int { return &n }

// The last week says "ends in N days" with the link; before it, the date
// alone; after it, that it ended.
func TestStatusPlanLine(t *testing.T) {
	const ends = "2026-10-01T10:00:00Z"
	for _, c := range []struct {
		name    string
		r       statusReport
		line    string
		upgrade string
	}{
		{"three days", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(3), UpgradeURL: statusUpgrade},
			"trial (ends in 3 days, Oct 1)", statusUpgrade},
		{"one day", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(1), UpgradeURL: statusUpgrade},
			"trial (ends in 1 day, Oct 1)", statusUpgrade},
		{"seven days", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(7), UpgradeURL: statusUpgrade},
			"trial (ends in 7 days, Oct 1)", statusUpgrade},
		{"twenty days", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(20), UpgradeURL: statusUpgrade},
			"trial (ends " + ends + ")", ""},
		{"ended", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(0), TrialEnded: true, UpgradeURL: statusUpgrade},
			"trial (ended Oct 1 — running apps keep serving, deploys are paused)", statusUpgrade},
		{"last week, no link from the server", statusReport{Plan: "trial", TrialEndsAt: ends, TrialDaysLeft: intp(3)},
			"trial (ends in 3 days, Oct 1)", ""},
		{"clock unknown", statusReport{Plan: "trial", TrialEndsAt: ends},
			"trial (ends " + ends + ")", ""},
		{"business", statusReport{Plan: "standard"}, "standard", ""},
	} {
		line, upgrade := c.r.planLine()
		if line != c.line || upgrade != c.upgrade {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, line, upgrade, c.line, c.upgrade)
		}
	}
}

// deploy-api's count and link win; without them the CLI counts by the same
// rule and has no link to print.
func TestStatusReadsTheTrialClock(t *testing.T) {
	r := statusReport{Plan: "trial", TrialEndsAt: time.Now().Add(3*24*time.Hour - time.Hour).UTC().Format(time.RFC3339)}
	r.readTrialClock(&apps.PlanStatus{DaysLeft: intp(3), UpgradeURL: statusUpgrade}, nil)
	if r.TrialDaysLeft == nil || *r.TrialDaysLeft != 3 || r.UpgradeURL != statusUpgrade {
		t.Fatalf("from the server: %+v", r)
	}
	r = statusReport{Plan: "trial", TrialEndsAt: r.TrialEndsAt}
	r.readTrialClock(nil, errors.New("404"))
	if r.TrialDaysLeft == nil || *r.TrialDaysLeft != 3 || r.UpgradeURL != "" {
		t.Fatalf("counted locally: %+v", r)
	}
}

// Every trial names dibbla upgrade under Plan: (DIB-1047); in the last week
// and after the end the console link comes first. Other plans get nothing new.
func TestStatusUpgradeLines(t *testing.T) {
	trial := statusReport{Plan: "trial", UpgradeCommand: upgradeCommand}
	if got := trial.upgradeLines(""); len(got) != 1 || got[0] != "upgrade: run dibbla upgrade for a payment link" {
		t.Fatalf("trial: %q", got)
	}
	if got := trial.upgradeLines(statusUpgrade); len(got) != 2 || got[0] != "upgrade: "+statusUpgrade || got[1] != "         or run dibbla upgrade for a payment link" {
		t.Fatalf("last week: %q", got)
	}
	if got := (&statusReport{Plan: "standard"}).upgradeLines(""); got != nil {
		t.Fatalf("business: %q", got)
	}
}
