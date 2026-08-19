package cmd

import (
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
)

// Setting DIBBLA_API_TOKEN keeps resolveOrgWithSource on its env-only branch,
// so these run the same with or without an OS keyring.

func TestResolveOrgWithSource_Env(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "org-env")

	id, _, source := resolveOrgWithSource()
	if id != "org-env" {
		t.Errorf("orgID = %q, want org-env", id)
	}
	if source != "env (DIBBLA_ORG_ID)" {
		t.Errorf("source = %q, want env (DIBBLA_ORG_ID)", source)
	}
}

func TestResolveOrgWithSource_FlagBeatsEnv(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "org-env")

	orig := config.FlagOrgID
	config.FlagOrgID = "org-flag"
	t.Cleanup(func() { config.FlagOrgID = orig })

	id, _, source := resolveOrgWithSource()
	if id != "org-flag" {
		t.Errorf("orgID = %q, want org-flag", id)
	}
	if source != "flag (--org)" {
		t.Errorf("source = %q, want flag (--org)", source)
	}
}

func TestResolveOrgWithSource_NoneIsAccountDefault(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "")

	orig := config.FlagOrgID
	config.FlagOrgID = ""
	t.Cleanup(func() { config.FlagOrgID = orig })

	id, name, source := resolveOrgWithSource()
	if id != "" || name != "" {
		t.Errorf("got (%q, %q), want both empty", id, name)
	}
	if !strings.Contains(source, "account default") {
		t.Errorf("source = %q, should say the account default applies", source)
	}
}

// The report is what `--json` prints; org must be in it, and absent rather
// than an empty string when nothing is selected.
func TestBuildStatusReport_IncludesOrg(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_ORG_ID", "org-env")

	orig := config.FlagOrgID
	config.FlagOrgID = ""
	t.Cleanup(func() { config.FlagOrgID = orig })

	r := buildStatusReport(true)
	if r.OrgID != "org-env" {
		t.Errorf("OrgID = %q, want org-env", r.OrgID)
	}
	if r.OrgSource != "env (DIBBLA_ORG_ID)" {
		t.Errorf("OrgSource = %q, want env (DIBBLA_ORG_ID)", r.OrgSource)
	}
}

func TestOrgCommand_IsRegistered(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "org" {
			found = true
			subs := map[string]bool{}
			for _, sc := range c.Commands() {
				subs[sc.Name()] = true
			}
			for _, want := range []string{"list", "use", "clear"} {
				if !subs[want] {
					t.Errorf("org subcommand %q not registered", want)
				}
			}
		}
	}
	if !found {
		t.Fatal("org command not registered on root")
	}
}

// The flag has to be persistent: `dibbla --org X deploy` and
// `dibbla deploy --org X` should both work, and `wf` defines its own
// PersistentPreRunE, which would shadow a root hook.
func TestOrgFlag_IsPersistentAndBoundToConfig(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("org")
	if f == nil {
		t.Fatal("--org is not a persistent flag on root")
	}

	orig := config.FlagOrgID
	t.Cleanup(func() {
		config.FlagOrgID = orig
		_ = f.Value.Set(orig)
	})

	if err := f.Value.Set("org-from-flag"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if config.FlagOrgID != "org-from-flag" {
		t.Errorf("config.FlagOrgID = %q, want org-from-flag", config.FlagOrgID)
	}
}
