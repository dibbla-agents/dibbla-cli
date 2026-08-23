package cmd

import (
	"os"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/credential/credtest"
)

// P-0011 Part C. Before named contexts the organization pin lived in one
// machine-wide slot, which was correct while there was exactly one server.
// With per-context servers it stops being correct: an organization id is
// issued by, and only means anything on, the server that issued it, so a
// machine-wide pin would send one server's organization to another.
//
// Nothing was broken before contexts existed. This is a defect the change
// would INTRODUCE if the pin stayed where it was, which is why it lands with
// the contexts rather than after them.

func orgIsolate(t *testing.T) *credtest.Fake {
	t.Helper()
	fake, _ := credtest.Install(t)
	for _, k := range []string{
		"DIBBLA_API_TOKEN", "DIBBLA_API_URL", "DIBBLA_AUTH_SERVICE_URL",
		"DIBBLA_ORG_ID", "DIBBLA_CONTEXT",
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_HOME", "BUILDKITE",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	oc, oo := config.ContextOverride, config.FlagOrgID
	config.ContextOverride, config.FlagOrgID = "", ""
	t.Cleanup(func() { config.ContextOverride, config.FlagOrgID = oc, oo })
	return fake
}

func seedTwoContexts(t *testing.T, fake *credtest.Fake, current string) {
	t.Helper()
	cfg := &contextcfg.Config{
		Current: current,
		Contexts: map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("prod")] = "tok-prod"
	fake.Items[credtest.ContextToken("haja")] = "tok-haja"
}

func TestStoreOrgPin_WritesToTheActiveContextOnly(t *testing.T) {
	fake := orgIsolate(t)
	seedTwoContexts(t, fake, "prod")

	if _, err := storeOrgPin(config.Load(), "org-prod", "Prod Inc"); err != nil {
		t.Fatal(err)
	}
	// Switch and pin a different organization.
	config.ContextOverride = "haja"
	if _, err := storeOrgPin(config.Load(), "org-haja", "Haja AB"); err != nil {
		t.Fatal(err)
	}

	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get("prod"); got.Org != "org-prod" || got.OrgName != "Prod Inc" {
		t.Errorf("prod's pin = %+v, want it untouched by the haja pin", got)
	}
	if got, _ := store.Get("haja"); got.Org != "org-haja" || got.OrgName != "Haja AB" {
		t.Errorf("haja's pin = %+v", got)
	}

	// And resolution follows the context, which is the failure this prevents:
	// prod's organization must never ride along to the other server.
	config.ContextOverride = "prod"
	if got := config.Load(); got.OrgID != "org-prod" {
		t.Errorf("prod resolves OrgID %q, want org-prod", got.OrgID)
	}
	config.ContextOverride = "haja"
	if got := config.Load(); got.OrgID != "org-haja" {
		t.Errorf("haja resolves OrgID %q, want org-haja — the wrong-org-to-the-wrong-server case", got.OrgID)
	}
}

func TestStoreOrgPin_DoesNotUseTheMachineWideSlotAsTheSourceOfTruth(t *testing.T) {
	fake := orgIsolate(t)
	seedTwoContexts(t, fake, "prod")

	if _, err := storeOrgPin(config.Load(), "org-prod", "Prod Inc"); err != nil {
		t.Fatal(err)
	}
	// The pin belongs to the context. The legacy machine-wide keys are written
	// too, but only as a mirror of whichever context is current — so switching
	// context must move them.
	if fake.Get("org_id") != "org-prod" {
		t.Errorf("legacy mirror org = %q, want org-prod", fake.Get("org_id"))
	}

	config.ContextOverride = ""
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Current = "haja"
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	config.SyncLegacyMirror()

	if fake.Get("org_id") != "" {
		t.Errorf("legacy mirror org = %q after switching to an unpinned context, want it cleared — a stale pin here is the exact wrong-org bug", fake.Get("org_id"))
	}
	if got := config.Load(); got.OrgID != "" {
		t.Errorf("OrgID = %q on an unpinned context, want empty so no X-Org-ID is sent at all", got.OrgID)
	}
}

func TestStoreOrgPin_ClearIsPerContext(t *testing.T) {
	fake := orgIsolate(t)
	seedTwoContexts(t, fake, "prod")

	if _, err := storeOrgPin(config.Load(), "org-prod", "Prod"); err != nil {
		t.Fatal(err)
	}
	config.ContextOverride = "haja"
	if _, err := storeOrgPin(config.Load(), "org-haja", "Haja"); err != nil {
		t.Fatal(err)
	}
	// Clear on haja only.
	if _, err := storeOrgPin(config.Load(), "", ""); err != nil {
		t.Fatal(err)
	}

	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get("haja"); got.Org != "" {
		t.Errorf("haja's pin = %q after clear, want empty", got.Org)
	}
	if got, _ := store.Get("prod"); got.Org != "org-prod" {
		t.Errorf("prod's pin = %q, want it untouched by a clear on another context", got.Org)
	}
}

func TestStoreOrgPin_NoContextAtAll_FallsBackToTheMachineWideSlot(t *testing.T) {
	// A machine that has never logged in has nowhere else to put the pin, and
	// the machine-wide slot is what the next command will read. This is the
	// pre-contexts behaviour, kept for exactly that case.
	fake := orgIsolate(t)

	where, err := storeOrgPin(config.Load(), "org-x", "X")
	if err != nil {
		t.Fatal(err)
	}
	if where != "keychain" {
		t.Errorf("stored in %q, want the keychain fallback", where)
	}
	if fake.Get("org_id") != "org-x" {
		t.Errorf("machine-wide org = %q, want org-x", fake.Get("org_id"))
	}
	if id, _, _ := credential.GetOrg(); id != "org-x" {
		t.Errorf("GetOrg = %q, want org-x", id)
	}
}
