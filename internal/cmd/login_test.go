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
// --browser flag definition.
func TestLoginFlag_BrowserExists(t *testing.T) {
	f := loginCmd.Flags().Lookup("browser")
	if f == nil {
		t.Fatal("expected --browser flag to be registered on loginCmd")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--browser flag type = %q, want bool", f.Value.Type())
	}
}

// TestLoginFlag_APIURLExists guards the --api-url flag.
func TestLoginFlag_APIURLExists(t *testing.T) {
	f := loginCmd.Flags().Lookup("api-url")
	if f == nil {
		t.Fatal("expected --api-url flag to be registered on loginCmd")
	}
	if f.Value.Type() != "string" {
		t.Errorf("--api-url flag type = %q, want string", f.Value.Type())
	}
}

// TestLoginFlag_WriteEnvExists guards the --write-env flag.
func TestLoginFlag_WriteEnvExists(t *testing.T) {
	f := loginCmd.Flags().Lookup("write-env")
	if f == nil {
		t.Fatal("expected --write-env flag to be registered on loginCmd")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--write-env flag type = %q, want bool", f.Value.Type())
	}
}

// TestLoginFlag_NoKeychainExists guards the --no-keychain flag.
func TestLoginFlag_NoKeychainExists(t *testing.T) {
	f := loginCmd.Flags().Lookup("no-keychain")
	if f == nil {
		t.Fatal("expected --no-keychain flag to be registered on loginCmd")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--no-keychain flag type = %q, want bool", f.Value.Type())
	}
}

// TestResolveLoginBaseURL_APIURLPositionalConflict verifies that giving
// both --api-url and a positional arg is rejected, rather than silently
// preferring one — ambiguity in an auth command is always worth an error.
func TestResolveLoginBaseURL_APIURLPositionalConflict(t *testing.T) {
	orig := loginAPIURL
	t.Cleanup(func() { loginAPIURL = orig })

	loginAPIURL = "https://api.dibbla.net"
	_, err := resolveLoginBaseURL([]string{"https://api.dibbla.com"})
	if err == nil {
		t.Fatal("expected error when both --api-url and positional arg given")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("error should mention both sources, got: %v", err)
	}
}

// TestResolveLoginBaseURL_APIURLFlagWinsOverEnv verifies that explicit
// --api-url takes precedence over $DIBBLA_API_URL — flag > env is the
// standard CLI precedence and the user expects it.
func TestResolveLoginBaseURL_APIURLFlagWinsOverEnv(t *testing.T) {
	orig := loginAPIURL
	t.Cleanup(func() { loginAPIURL = orig })

	t.Setenv("DIBBLA_API_URL", "https://api.from.env")
	loginAPIURL = "https://api.from.flag"

	got, err := resolveLoginBaseURL(nil)
	if err != nil {
		t.Fatalf("resolveLoginBaseURL: %v", err)
	}
	if got != "https://api.from.flag" {
		t.Errorf("got %q, want flag value", got)
	}
}
