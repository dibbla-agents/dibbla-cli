package render

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestLog_Happy(t *testing.T) {
	var out, errBuf bytes.Buffer
	r := NewLog(&out, &errBuf)
	scriptedHappy(r)
	if code := r.OnDone(); code != 0 {
		t.Fatalf("OnDone = %d, want 0", code)
	}

	stdout := out.String()
	// One line per event, ISO-8601 timestamp, level tag, scope, msg shape.
	rfc3339 := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)
	for i, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		if i == 0 && !rfc3339.MatchString(line) {
			t.Errorf("first line missing RFC3339 timestamp: %q", line)
		}
	}
	for _, want := range []string{
		"[info]",
		"build",
		"name=go-build",
		"deploy ok",
		"https://analytics-api.dibbla.com",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected stdout to contain %q\n--- stdout ---\n%s", want, stdout)
		}
	}
	// Successful deploy must not pollute stderr with the structured JSON
	// event — that's reserved for failures.
	if errBuf.Len() != 0 {
		t.Errorf("expected empty stderr on success, got: %s", errBuf.String())
	}
}

func TestLog_Failure(t *testing.T) {
	var out, errBuf bytes.Buffer
	r := NewLog(&out, &errBuf)
	scriptedFailure(r)
	if code := r.OnDone(); code != 2 {
		t.Fatalf("OnDone = %d, want 2", code)
	}

	stdout := out.String()
	if !strings.Contains(stdout, "deploy failed") {
		t.Errorf("expected `deploy failed` summary on stdout\n--- stdout ---\n%s", stdout)
	}
	if !strings.Contains(stdout, "BUILD OUTPUT · step 1/5 (go-build)") {
		t.Errorf("expected fenced build output block\n--- stdout ---\n%s", stdout)
	}
	if !strings.Contains(stdout, "internal/api/router.go:42:18: undefined: store.NewPostgres") {
		t.Errorf("expected parsed compile error in stdout\n--- stdout ---\n%s", stdout)
	}

	// Structured JSON event lands on stderr — one line, parseable.
	var ev structuredFailureEvent
	if err := json.Unmarshal(bytes.TrimSpace(errBuf.Bytes()), &ev); err != nil {
		t.Fatalf("stderr is not parseable JSON: %v\n%s", err, errBuf.String())
	}
	if ev.Event != "deploy.failed" {
		t.Errorf("event = %q, want deploy.failed", ev.Event)
	}
	if ev.Step != "go-build" {
		t.Errorf("step = %q, want go-build", ev.Step)
	}
	if ev.RetryCmd == "" {
		t.Errorf("expected retry_cmd to be populated")
	}
	if len(ev.Errors) != 1 || ev.Errors[0].File != "internal/api/router.go" {
		t.Errorf("expected one parsed error for router.go, got %+v", ev.Errors)
	}
}
