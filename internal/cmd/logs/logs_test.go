package logs

import (
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/applogs"
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

func TestFormatEntry_JSONLine(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2025-04-29T10:00:00.123Z")
	e := applogs.Entry{
		Timestamp: ts,
		Line:      `{"level":"ERROR","msg":"boom"}`,
	}
	got := formatEntry(e, false)
	// Avoid testing the local-time hour (tz-dependent); test the level + msg.
	if !strings.Contains(got, "ERROR") {
		t.Errorf("formatEntry = %q, want ERROR tag", got)
	}
	if !strings.HasSuffix(got, "boom") {
		t.Errorf("formatEntry = %q, want trailing msg", got)
	}
}

func TestFormatEntry_PlainLine(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2025-04-29T10:00:00.123Z")
	e := applogs.Entry{
		Timestamp: ts,
		Line:      "raw|pipe|delimited",
	}
	got := formatEntry(e, false)
	if !strings.HasSuffix(got, "raw|pipe|delimited") {
		t.Errorf("formatEntry = %q, want trailing raw line", got)
	}
}

func TestFlagDefaults(t *testing.T) {
	// Reset to defaults (cobra binds to package globals via init()).
	if logsCmd.Flags().Lookup("since").DefValue != "15m0s" {
		t.Errorf("--since default = %s, want 15m0s", logsCmd.Flags().Lookup("since").DefValue)
	}
	if logsCmd.Flags().Lookup("follow").DefValue != "false" {
		t.Errorf("--follow default should be false")
	}
	if logsCmd.Flags().Lookup("tail").DefValue != "0" {
		t.Errorf("--tail default should be 0")
	}
}
