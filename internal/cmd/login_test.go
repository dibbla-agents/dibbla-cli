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
	// Force the non-SSH branch so this test exercises the
	// general-purpose error message regardless of where it runs.
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")

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
	// Sanity: the error must point at the live api-keys page, not the
	// long-dead /settings/api-tokens path.
	if strings.Contains(msg, "/settings/api-tokens") {
		t.Errorf("error message points at dead URL /settings/api-tokens:\n%s", msg)
	}
	if !strings.Contains(msg, "/api-keys") {
		t.Errorf("error message should link to /api-keys:\n%s", msg)
	}
}

// TestAcquireToken_NonInteractiveSSH_OmitsBrowserHint verifies that when
// running over SSH, the recovery message does NOT advertise --browser
// (which would lead the user into a 5-minute callback timeout) and DOES
// advertise --api-key plus the env var. This is the headline UX fix
// for SSH login from v1.2.20 onward.
func TestAcquireToken_NonInteractiveSSH_OmitsBrowserHint(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "10.0.0.1 1234 10.0.0.2 22")
	t.Setenv("SSH_TTY", "")

	_, err := acquireToken("https://api.dibbla.com")
	if err == nil {
		t.Fatal("expected acquireToken to reject non-TTY stdin")
	}
	msg := err.Error()
	if strings.Contains(msg, "--browser") {
		t.Errorf("SSH error message must not advertise --browser:\n%s", msg)
	}
	for _, want := range []string{"--api-key", "DIBBLA_API_TOKEN", "/api-keys"} {
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
