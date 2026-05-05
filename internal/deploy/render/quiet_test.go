package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestQuiet_Happy(t *testing.T) {
	var buf bytes.Buffer
	r := NewQuiet(&buf)
	scriptedHappy(r)
	if code := r.OnDone(); code != 0 {
		t.Fatalf("OnDone = %d, want 0", code)
	}
	out := strings.TrimSpace(buf.String())
	// One line, marker + alias + URL.
	if strings.Count(out, "\n") != 0 {
		t.Errorf("expected exactly one output line, got:\n%s", out)
	}
	for _, want := range []string{"✓", "analytics-api", "https://analytics-api.dibbla.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %s", want, out)
		}
	}
}

func TestQuiet_Failure(t *testing.T) {
	var buf bytes.Buffer
	r := NewQuiet(&buf)
	scriptedFailure(r)
	if code := r.OnDone(); code != 2 {
		t.Fatalf("OnDone = %d, want 2", code)
	}
	if !strings.Contains(buf.String(), "BUILD_FAILED") {
		t.Errorf("expected BUILD_FAILED in output, got: %s", buf.String())
	}
}
