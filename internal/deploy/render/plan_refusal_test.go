package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const (
	trialLink = "https://console.dibbla.com/org-settings/plan?upgrade=review"
	trialText = "Your free trial has ended — thanks for trying Dibbla!\n" +
		"Your apps keep running exactly as they are. To deploy again, upgrade to Business (1\u00a0000 kr / €89 / $100 per month, VAT included):\n" +
		"  " + trialLink
)

// scriptedTrialExpired is the server's TRIAL_EXPIRED answer (DIB-1045): a
// pre-build refusal carrying the upgrade link.
func scriptedTrialExpired(r Renderer) {
	r.OnEvent(DeployEvent{
		Type: "error",
		Error: &DeployError{
			APIError: &APIError{Code: "TRIAL_EXPIRED", Message: trialText,
				Documentation: "https://docs.dibbla.com/reference/plans", UpgradeURL: trialLink},
			StatusCode: 403,
		},
	})
}

func TestTTY_TrialExpiredIsACalmNoteWithTheLink(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	scriptedTrialExpired(r)
	if code := r.OnDone(); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	out := buf.String()
	for _, want := range []string{
		"● Your free trial has ended — thanks for trying Dibbla!",
		"    Your apps keep running exactly as they are.",
		"      " + trialLink + "\n",
		"Docs: https://docs.dibbla.com/reference/plans",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "✗") || strings.Contains(out, "TRIAL_EXPIRED") {
		t.Errorf("a plan refusal is drawn as a failure:\n%s", out)
	}
	if strings.Index(out, trialLink) > strings.Index(out, "Docs:") {
		t.Errorf("the Docs line comes before the link:\n%s", out)
	}
}

// With ANSI on, the link is an OSC 8 hyperlink whose visible text is the URL.
func TestTTY_TrialExpiredLinkIsClickable(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, true)
	scriptedTrialExpired(r)
	r.OnDone()
	if !strings.Contains(buf.String(), "\033]8;;"+trialLink+"\033\\") {
		t.Fatalf("no OSC 8 hyperlink:\n%q", buf.String())
	}
}

func TestLog_TrialExpiredPrintsTheTextAsWritten(t *testing.T) {
	var out, errw bytes.Buffer
	r := NewLog(&out, &errw)
	scriptedTrialExpired(r)
	r.OnDone()
	got := out.String()
	for _, want := range []string{trialText + "\n", "Docs: https://docs.dibbla.com/reference/plans"} {
		if !strings.Contains(got, want) {
			t.Errorf("plain output lacks %q:\n%s", want, got)
		}
	}
	var ev structuredFailureEvent
	if err := json.Unmarshal(errw.Bytes(), &ev); err != nil || ev.UpgradeURL != trialLink {
		t.Fatalf("stderr event lacks upgrade_url (%v): %s", err, errw.String())
	}
}

func TestJSON_TrialExpiredCarriesUpgradeURL(t *testing.T) {
	var buf bytes.Buffer
	r := NewJSON(&buf)
	scriptedTrialExpired(r)
	r.OnDone()
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if got["upgrade_url"] != trialLink || got["api_error_code"] != "TRIAL_EXPIRED" || got["documentation"] == nil {
		t.Fatalf("--json: %s", buf.String())
	}
}

// A failure that is not a plan refusal keeps the red "✗ CODE: message".
func TestTTY_OtherFailuresAreUnchanged(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	r.OnEvent(DeployEvent{Type: "error", Error: &DeployError{APIError: &APIError{Code: "ROLE_FORBIDDEN", Message: "your role cannot deploy"}}})
	r.OnDone()
	if !strings.Contains(buf.String(), "✗ ROLE_FORBIDDEN: your role cannot deploy") {
		t.Fatalf("output:\n%s", buf.String())
	}
}
