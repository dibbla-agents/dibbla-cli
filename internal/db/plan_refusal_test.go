package db

import (
	"errors"
	"strings"
	"testing"
)

// db create after the trial ended returns the server's note with the link and
// the Docs line (DIB-1045); other errors keep "CODE: message".
func TestParseError_PlanRefusal(t *testing.T) {
	body := `{"status":"error","error":{"code":"TRIAL_EXPIRED","message":"Your free trial has ended — thanks for trying Dibbla!\nYour apps keep running exactly as they are. To create databases again, upgrade to Business:\n  https://c/org-settings/plan?upgrade=review","documentation":"https://docs.dibbla.com/reference/plans","upgrade_url":"https://c/org-settings/plan?upgrade=review"}}`
	err := parseError([]byte(body), 403)
	var refusal *PlanRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %T %v", err, err)
	}
	got := err.Error()
	if !strings.Contains(got, "To create databases again") || !strings.HasSuffix(got, "\n\nDocs: https://docs.dibbla.com/reference/plans") ||
		strings.Count(got, "https://c/org-settings/plan?upgrade=review") != 1 || strings.Contains(got, "TRIAL_EXPIRED") {
		t.Fatalf("text:\n%s", got)
	}

	err = parseError([]byte(`{"error":{"code":"DATABASE_EXISTS","message":"exists"}}`), 409)
	if errors.As(err, &refusal) || err.Error() != "DATABASE_EXISTS: exists" {
		t.Fatalf("other error: %v", err)
	}
}
