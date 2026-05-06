package applogs

import (
	"strings"
	"testing"
	"time"
)

func TestSplitLevelAndMessage_JSON(t *testing.T) {
	level, msg := splitLevelAndMessage(`{"time":"2025-01-01T00:00:00Z","level":"INFO","msg":"hello world","extra":1}`)
	if level != "INFO" {
		t.Errorf("level = %q, want INFO", level)
	}
	if msg != "hello world" {
		t.Errorf("msg = %q, want hello world", msg)
	}
}

func TestSplitLevelAndMessage_Plain(t *testing.T) {
	level, msg := splitLevelAndMessage("just a regular line")
	if level != "" {
		t.Errorf("level = %q, want empty", level)
	}
	if msg != "just a regular line" {
		t.Errorf("msg = %q, want raw line", msg)
	}
}

func TestSplitLevelAndMessage_Bracketed(t *testing.T) {
	// Workflow run-logs endpoint emits "[LEVEL] message" lines.
	level, msg := splitLevelAndMessage("[INFO] hello from runlog")
	if level != "INFO" {
		t.Errorf("level = %q, want INFO", level)
	}
	if msg != "hello from runlog" {
		t.Errorf("msg = %q, want hello from runlog", msg)
	}

	// Bracketed but unknown word — leave as-is.
	level, msg = splitLevelAndMessage("[NOTALEVEL] body")
	if level != "" || msg != "[NOTALEVEL] body" {
		t.Errorf("non-level bracket should pass through: level=%q msg=%q", level, msg)
	}
}

func TestFormatEntry_JSONLine(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2025-04-29T10:00:00.123Z")
	e := Entry{
		Timestamp: ts,
		Line:      `{"level":"ERROR","msg":"boom"}`,
	}
	got := FormatEntry(e, false)
	if !strings.Contains(got, "ERROR") {
		t.Errorf("FormatEntry = %q, want ERROR tag", got)
	}
	if !strings.HasSuffix(got, "boom") {
		t.Errorf("FormatEntry = %q, want trailing msg", got)
	}
}

func TestFormatEntry_PlainLine(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2025-04-29T10:00:00.123Z")
	e := Entry{
		Timestamp: ts,
		Line:      "raw|pipe|delimited",
	}
	got := FormatEntry(e, false)
	if !strings.HasSuffix(got, "raw|pipe|delimited") {
		t.Errorf("FormatEntry = %q, want trailing raw line", got)
	}
}

func TestFormatEntry_LevelFromLabels(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2025-04-29T10:00:00.123Z")
	e := Entry{
		Timestamp: ts,
		Line:      "plain message",
		Labels:    map[string]string{"level": "WARN"},
	}
	got := FormatEntry(e, false)
	if !strings.Contains(got, "WARN") {
		t.Errorf("FormatEntry = %q, want WARN tag from labels.level", got)
	}
}
