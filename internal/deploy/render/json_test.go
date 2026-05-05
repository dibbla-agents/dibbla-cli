package render

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSON_Happy(t *testing.T) {
	var buf bytes.Buffer
	r := NewJSON(&buf)
	scriptedHappy(r)
	if code := r.OnDone(); code != 0 {
		t.Fatalf("OnDone = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if got["alias"] != "analytics-api" {
		t.Errorf("alias = %v, want analytics-api", got["alias"])
	}
	if got["url"] != "https://analytics-api.dibbla.com" {
		t.Errorf("url = %v", got["url"])
	}
}

func TestJSON_Failure(t *testing.T) {
	var buf bytes.Buffer
	r := NewJSON(&buf)
	scriptedFailure(r)
	if code := r.OnDone(); code != 2 {
		t.Fatalf("OnDone = %d, want 2", code)
	}
	var ev structuredFailureEvent
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if ev.Event != "deploy.failed" {
		t.Errorf("event = %q, want deploy.failed", ev.Event)
	}
	if ev.Step != "go-build" {
		t.Errorf("step = %q, want go-build", ev.Step)
	}
}
