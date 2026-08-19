package config

import "testing"

// These exercise the env-only path (DIBBLA_API_TOKEN set), which is the branch
// that never consults the keyring — so the tests run identically on a laptop
// with a keychain and in CI without one.

func TestLoad_OrgFromEnv(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "org-env")

	if got := Load().OrgID; got != "org-env" {
		t.Errorf("OrgID = %q, want org-env", got)
	}
}

func TestLoad_FlagBeatsEnv(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "org-env")

	orig := FlagOrgID
	FlagOrgID = "org-flag"
	t.Cleanup(func() { FlagOrgID = orig })

	if got := Load().OrgID; got != "org-flag" {
		t.Errorf("OrgID = %q, want org-flag — --org must win over the environment", got)
	}
}

// No org is a valid state, not a missing one: the header is then omitted and
// the API falls back to the account's default org.
func TestLoad_OrgEmptyByDefault(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "")

	orig := FlagOrgID
	FlagOrgID = ""
	t.Cleanup(func() { FlagOrgID = orig })

	cfg := Load()
	if cfg.OrgID != "" {
		t.Errorf("OrgID = %q, want empty", cfg.OrgID)
	}
	if cfg.OrgName != "" {
		t.Errorf("OrgName = %q, want empty", cfg.OrgName)
	}
}

func TestLoad_OrgIsTrimmed(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "  org-padded \n")

	orig := FlagOrgID
	FlagOrgID = ""
	t.Cleanup(func() { FlagOrgID = orig })

	if got := Load().OrgID; got != "org-padded" {
		t.Errorf("OrgID = %q, want org-padded", got)
	}
}
