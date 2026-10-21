package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const (
	planLink = "https://console.dibbla.com/org-settings/plan?upgrade=review"
	planText = "This organization is at the Hobby plan's limit of 3 apps.\n" +
		"Your apps keep running exactly as they are. To add another, remove one or upgrade to Business (1\u00a0000 kr / €89 / $100 per month, VAT included):\n" +
		"  " + planLink
)

// scriptedPlanRefusal is the server's PLAN_LIMIT_EXCEEDED answer for a Hobby
// org (DIB-1045): a pre-build refusal carrying the upgrade link.
func scriptedPlanRefusal(r Renderer) {
	r.OnEvent(DeployEvent{
		Type: "error",
		Error: &DeployError{
			APIError: &APIError{Code: "PLAN_LIMIT_EXCEEDED", Message: planText,
				Documentation: "https://docs.dibbla.com/reference/plans", UpgradeURL: planLink},
			StatusCode: 403,
		},
	})
}

func TestTTY_PlanRefusalIsACalmNoteWithTheLink(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	scriptedPlanRefusal(r)
	if code := r.OnDone(); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	out := buf.String()
	for _, want := range []string{
		"● This organization is at the Hobby plan's limit of 3 apps.",
		"    Your apps keep running exactly as they are.",
		"      " + planLink + "\n",
		"    Or get the payment link right here: dibbla upgrade\n",
		"Docs: https://docs.dibbla.com/reference/plans",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "✗") || strings.Contains(out, "PLAN_LIMIT_EXCEEDED") {
		t.Errorf("a plan refusal is drawn as a failure:\n%s", out)
	}
	if strings.Index(out, planLink) > strings.Index(out, "Docs:") {
		t.Errorf("the Docs line comes before the link:\n%s", out)
	}
}

// With ANSI on, the link is an OSC 8 hyperlink whose visible text is the URL.
func TestTTY_PlanRefusalLinkIsClickable(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, true)
	scriptedPlanRefusal(r)
	r.OnDone()
	if !strings.Contains(buf.String(), "\033]8;;"+planLink+"\033\\") {
		t.Fatalf("no OSC 8 hyperlink:\n%q", buf.String())
	}
}

func TestLog_PlanRefusalPrintsTheTextAsWritten(t *testing.T) {
	var out, errw bytes.Buffer
	r := NewLog(&out, &errw)
	scriptedPlanRefusal(r)
	r.OnDone()
	got := out.String()
	for _, want := range []string{planText + "\n", "Or get the payment link right here: dibbla upgrade\n", "Docs: https://docs.dibbla.com/reference/plans"} {
		if !strings.Contains(got, want) {
			t.Errorf("plain output lacks %q:\n%s", want, got)
		}
	}
	var ev structuredFailureEvent
	if err := json.Unmarshal(errw.Bytes(), &ev); err != nil || ev.UpgradeURL != planLink {
		t.Fatalf("stderr event lacks upgrade_url (%v): %s", err, errw.String())
	}
}

func TestJSON_PlanRefusalCarriesUpgradeURL(t *testing.T) {
	var buf bytes.Buffer
	r := NewJSON(&buf)
	scriptedPlanRefusal(r)
	r.OnDone()
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if got["upgrade_url"] != planLink || got["api_error_code"] != "PLAN_LIMIT_EXCEEDED" || got["documentation"] == nil || got["upgrade_command"] != "dibbla upgrade" {
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
