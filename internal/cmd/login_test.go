package cmd

import (
	"strings"
	"testing"
)

// TestAcquireToken_NonInteractiveError_MentionsAllOptions verifies that the
// TTY-check error surfaces all three opt-out paths (--browser, --api-key,
// env var) so users invoking from Claude Code or other non-TTY shells can
// recover without reading docs.
func TestAcquireToken_NonInteractiveError_MentionsAllOptions(t *testing.T) {
	// In `go test`, stdin is not a TTY by default, so acquireToken returns
	// the error path we want to assert on.
	_, err := acquireToken("https://api.dibbla.com")
	if err == nil {
		t.Fatal("expected acquireToken to reject non-TTY stdin")
	}
	msg := err.Error()
	for _, want := range []string{"--browser", "--api-key", "DIBBLA_API_TOKEN"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q hint:\n%s", want, msg)
		}
	}
}

// TestLoginFlag_BrowserExists guards against an accidental removal of the
// --browser flag definition — it's the public surface of Phase 3.
func TestLoginFlag_BrowserExists(t *testing.T) {
	f := loginCmd.Flags().Lookup("browser")
	if f == nil {
		t.Fatal("expected --browser flag to be registered on loginCmd")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--browser flag type = %q, want bool", f.Value.Type())
	}
}
