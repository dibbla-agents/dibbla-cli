package db

import (
	"errors"
	"strings"
	"testing"
)

// db create over the Hobby plan's limit returns the server's note with the link and
// the Docs line (DIB-1045); other errors keep "CODE: message".
func TestParseError_PlanRefusal(t *testing.T) {
	body := `{"status":"error","error":{"code":"PLAN_LIMIT_EXCEEDED","message":"This organization is at the Hobby plan's limit of 2 databases.\nYour databases keep running exactly as they are. To create another, remove one or upgrade to Business:\n  https://c/org-settings/plan?upgrade=review","documentation":"https://docs.dibbla.com/reference/plans","upgrade_url":"https://c/org-settings/plan?upgrade=review"}}`
	err := parseError([]byte(body), 403)
	var refusal *PlanRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %T %v", err, err)
	}
	got := err.Error()
	if !strings.Contains(got, "To create another") || !strings.Contains(got, "\nOr get the payment link right here: dibbla upgrade\n") || !strings.HasSuffix(got, "\n\nDocs: https://docs.dibbla.com/reference/plans") ||
		strings.Count(got, "https://c/org-settings/plan?upgrade=review") != 1 || strings.Contains(got, "PLAN_LIMIT_EXCEEDED") {
		t.Fatalf("text:\n%s", got)
	}

	err = parseError([]byte(`{"error":{"code":"DATABASE_EXISTS","message":"exists"}}`), 409)
	if errors.As(err, &refusal) || err.Error() != "DATABASE_EXISTS: exists" {
		t.Fatalf("other error: %v", err)
	}
}
