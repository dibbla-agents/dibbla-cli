package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const headsUp = "Heads-up: your free trial ends in 5 days (Oct 1). Your apps keep running after that, " +
	"but deploys pause until you upgrade:\n  " + trialLink

// scriptedDeployWithTrialWarning is a successful deploy in a trial's last
// week (DIB-1048).
func scriptedDeployWithTrialWarning(r Renderer, w *TrialWarning) {
	r.OnEvent(DeployEvent{Type: "result", Result: &DeployResult{
		Status:       "success",
		Deployment:   ResultDeployment{ID: "dep_1", Alias: "shop", URL: "https://shop.dibbla.app", Status: "running"},
		TrialWarning: w,
	}})
}

func lastWeek() *TrialWarning {
	return &TrialWarning{DaysLeft: 5, EndsAt: "2026-10-01T10:00:00Z", UpgradeURL: trialLink, Message: headsUp}
}

func TestTTY_TrialWarningEndsASuccessfulDeployOnce(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	scriptedDeployWithTrialWarning(r, lastWeek())
	if code := r.OnDone(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := buf.String()
	want := "\n  Heads-up: your free trial ends in 5 days (Oct 1). Your apps keep running\n" +
		"  after that, but deploys pause until you upgrade:\n" +
		"  " + trialLink + "\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("the deploy does not end with the heads-up:\n%s", out)
	}
	if strings.Count(out, "Heads-up") != 1 || strings.Count(out, trialLink) != 1 {
		t.Errorf("the heads-up is not drawn exactly once:\n%s", out)
	}
	if strings.Contains(out, "✗") || strings.Contains(out, "⚠") {
		t.Errorf("the heads-up is drawn as a problem:\n%s", out)
	}
}

func TestTTY_NoTrialWarningLeavesTheDeployAsItWas(t *testing.T) {
	var with, without bytes.Buffer
	r := NewTTY(&without, false)
	scriptedDeployWithTrialWarning(r, nil)
	r.OnDone()
	if strings.Contains(without.String(), "Heads-up") || strings.Contains(without.String(), "trial") {
		t.Fatalf("no warning, yet:\n%s", without.String())
	}
	r = NewTTY(&with, false)
	scriptedDeployWithTrialWarning(r, lastWeek())
	r.OnDone()
	if !strings.HasPrefix(with.String(), without.String()) {
		t.Fatalf("the warning changed what came before it:\n%s\n---\n%s", without.String(), with.String())
	}
}

func TestLogQuietJSON_CarryTheTrialWarning(t *testing.T) {
	var logOut bytes.Buffer
	l := NewLog(&logOut, &logOut)
	scriptedDeployWithTrialWarning(l, lastWeek())
	l.OnDone()
	if !strings.Contains(logOut.String(), "[info] trial      Heads-up: your free trial ends in 5 days (Oct 1).") ||
		!strings.Contains(logOut.String(), "[info] trial      "+trialLink) {
		t.Errorf("log:\n%s", logOut.String())
	}

	var quietOut bytes.Buffer
	q := NewQuiet(&quietOut)
	scriptedDeployWithTrialWarning(q, lastWeek())
	q.OnDone()
	if !strings.HasSuffix(quietOut.String(), "· Heads-up: your free trial ends in 5 days (Oct 1). Your apps keep running after that, but deploys pause until you upgrade:\n  "+trialLink+"\n") {
		t.Errorf("quiet:\n%s", quietOut.String())
	}

	var jsonOut bytes.Buffer
	j := NewJSON(&jsonOut)
	scriptedDeployWithTrialWarning(j, lastWeek())
	j.OnDone()
	var doc struct {
		TrialWarning *TrialWarning `json:"trial_warning"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &doc); err != nil || doc.TrialWarning == nil || doc.TrialWarning.DaysLeft != 5 || doc.TrialWarning.UpgradeURL != trialLink {
		t.Errorf("json: %v %s", err, jsonOut.String())
	}
}

// Without a console link the sentence is all there is, and nothing is lost.
func TestTrialWarningSplitWithoutLink(t *testing.T) {
	w := &TrialWarning{Message: "Heads-up: your free trial ends in 1 day (Oct 1). Your apps keep running after that, but deploys pause until you upgrade in the console (Org settings → Plan)."}
	text, url := w.Split()
	if url != "" || text != w.Message {
		t.Fatalf("text=%q url=%q", text, url)
	}
}
