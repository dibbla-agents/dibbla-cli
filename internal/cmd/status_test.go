package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildStatusReport_EnvURLAndTokenWin verifies the documented precedence:
// env vars beat any stored credential, and the source string reflects that.
// We pass --no-validate equivalent (true) so the test doesn't make a network
// call.
func TestBuildStatusReport_EnvURLAndTokenWin(t *testing.T) {
	t.Setenv("DIBBLA_API_URL", "https://api.example.test")
	t.Setenv("DIBBLA_API_TOKEN", "tok-from-env")
	// Ensure DIBBLA_AUTH_SERVICE_URL doesn't shadow DIBBLA_API_URL.
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "")

	r := buildStatusReport(true)

	if r.APIURL != "https://api.example.test" {
		t.Errorf("APIURL = %q, want %q", r.APIURL, "https://api.example.test")
	}
	if r.APIURLSource != "env (DIBBLA_API_URL)" {
		t.Errorf("APIURLSource = %q, want env (DIBBLA_API_URL)", r.APIURLSource)
	}
	if !r.TokenConfigured {
		t.Errorf("TokenConfigured = false, want true")
	}
	if r.TokenSource != "env (DIBBLA_API_TOKEN)" {
		t.Errorf("TokenSource = %q, want env (DIBBLA_API_TOKEN)", r.TokenSource)
	}
	if r.Validated {
		t.Errorf("Validated = true, expected skip when called with noValidate")
	}
}

// TestBuildStatusReport_AuthServiceURLFallback covers the dibbla-tasks
// steprunner case: DIBBLA_API_URL unset, DIBBLA_AUTH_SERVICE_URL set.
func TestBuildStatusReport_AuthServiceURLFallback(t *testing.T) {
	t.Setenv("DIBBLA_API_URL", "")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "https://auth.example.test")
	t.Setenv("DIBBLA_API_TOKEN", "tok")

	r := buildStatusReport(true)

	if r.APIURL != "https://auth.example.test" {
		t.Errorf("APIURL = %q, want https://auth.example.test", r.APIURL)
	}
	if r.APIURLSource != "env (DIBBLA_AUTH_SERVICE_URL)" {
		t.Errorf("APIURLSource = %q, want env (DIBBLA_AUTH_SERVICE_URL)", r.APIURLSource)
	}
}

// TestBuildStatusReport_NormalizesBareHost ensures hosts without a scheme
// get https:// prepended (matches login's normalization).
func TestBuildStatusReport_NormalizesBareHost(t *testing.T) {
	t.Setenv("DIBBLA_API_URL", "api.dibbla.net")
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "")

	r := buildStatusReport(true)
	if r.APIURL != "https://api.dibbla.net" {
		t.Errorf("APIURL = %q, want https://api.dibbla.net", r.APIURL)
	}
}

// TestBuildStatusReport_JSONShape ensures the JSON marshaling of the
// status report exposes a stable schema.
func TestBuildStatusReport_JSONShape(t *testing.T) {
	t.Setenv("DIBBLA_API_URL", "https://api.example.test")
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_AUTH_SERVICE_URL", "")

	r := buildStatusReport(true)
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"version"`,
		`"api_url"`,
		`"api_url_source"`,
		`"token_configured"`,
		`"token_source"`,
		`"validated"`,
		`"logged_in"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("JSON missing field %s: %s", want, out)
		}
	}
	// validation_error has omitempty — should be absent on a no-validate
	// call (no error was set).
	if strings.Contains(string(out), `"validation_error"`) {
		t.Errorf("JSON should omit validation_error when empty: %s", out)
	}
}

// TestStatusFlag_JSONExists guards against accidental flag removal.
func TestStatusFlag_JSONExists(t *testing.T) {
	if statusCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag missing on statusCmd")
	}
	if statusCmd.Flags().Lookup("no-validate") == nil {
		t.Fatal("--no-validate flag missing on statusCmd")
	}
}
