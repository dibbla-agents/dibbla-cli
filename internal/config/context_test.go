package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// --- Test harness ------------------------------------------------------------

// fakeKeyring is an in-memory stand-in for the OS credential store.
//
// It replaces the three credential.Keyring* seams rather than using
// go-keyring's own MockInit, which is process-global, offers no per-test
// isolation and — the reason that matters here — has no way to make a single
// operation fail. Several guarantees in this package are about ORDERING (a
// token is written under the new key before the old one is deleted), and an
// ordering guarantee can only be tested by interrupting it.
type fakeKeyring struct {
	items map[string]string
	// failSet, when non-nil, decides whether a Set should fail. It is
	// consulted per key, so a test can let the write that must happen first
	// succeed and fail the one that must happen second.
	failSet func(key string) error
	// failDelete does the same for Delete.
	failDelete func(key string) error
	// deleted records every key Delete was called on, in order, so a test can
	// assert not just the end state but the sequence that produced it.
	deleted []string
}

func newFakeKeyring(t *testing.T) *fakeKeyring {
	t.Helper()
	fk := &fakeKeyring{items: map[string]string{}}

	origGet, origSet, origDel := credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete
	credential.KeyringGet = func(service, key string) (string, error) {
		v, ok := fk.items[service+"/"+key]
		if !ok {
			// Must be the real sentinel: credential.get() maps
			// keyring.ErrNotFound to ("", nil) and anything else to a hard
			// error, so returning a look-alike here would turn every absent
			// key into a failure and quietly disable the paths under test.
			return "", keyring.ErrNotFound
		}
		return v, nil
	}
	credential.KeyringSet = func(service, key, value string) error {
		if fk.failSet != nil {
			if err := fk.failSet(key); err != nil {
				return err
			}
		}
		fk.items[service+"/"+key] = value
		return nil
	}
	credential.KeyringDelete = func(service, key string) error {
		fk.deleted = append(fk.deleted, key)
		if fk.failDelete != nil {
			if err := fk.failDelete(key); err != nil {
				return err
			}
		}
		delete(fk.items, service+"/"+key)
		return nil
	}
	t.Cleanup(func() {
		credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete = origGet, origSet, origDel
	})
	return fk
}

// has reports whether a key is present, ignoring the service prefix.
func (f *fakeKeyring) has(key string) bool {
	_, ok := f.items["dibbla-cli/"+key]
	return ok
}

func (f *fakeKeyring) get(key string) string { return f.items["dibbla-cli/"+key] }

// noKeyring makes every keyring operation behave as it does on a Linux host
// with no libsecret: the read fails with the exact wording credential's own
// IsKeyringUnavailable matches on, so the file-fallback path is exercised for
// the same reason it fires in production rather than by a test-only flag.
func noKeyring(t *testing.T) {
	t.Helper()
	unavailable := errors.New("failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'")
	origGet, origSet, origDel := credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete
	credential.KeyringGet = func(string, string) (string, error) { return "", unavailable }
	credential.KeyringSet = func(string, string, string) error { return unavailable }
	credential.KeyringDelete = func(string, string) error { return unavailable }
	t.Cleanup(func() {
		credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete = origGet, origSet, origDel
	})
}

// isolate gives a test its own config directory, its own keyring, a cleared
// environment and reset package state. Everything this package reads is either
// isolated here or explicitly set by the test — nothing is inherited from the
// developer's machine, which is what makes the resolution matrix below mean
// anything.
func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))

	for _, k := range []string{
		"DIBBLA_API_TOKEN", "DIBBLA_API_URL", "DIBBLA_AUTH_SERVICE_URL",
		"DIBBLA_ORG_ID", "DIBBLA_CONTEXT",
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_HOME", "BUILDKITE",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	origCtx, origOrg := ContextOverride, FlagOrgID
	ContextOverride, FlagOrgID = "", ""
	t.Cleanup(func() { ContextOverride, FlagOrgID = origCtx, origOrg })

	// Fail loudly if production code takes the exit path during a test that
	// did not opt into it. A silently-swallowed contextFailure would make a
	// test that should have failed look green.
	origFail := contextFailure
	contextFailure = func(err error) { t.Fatalf("unexpected context failure: %v", err) }
	t.Cleanup(func() { contextFailure = origFail })

	return dir
}

// captureFailure replaces the exit handler so a test can assert on the error
// Load() reports instead of the process dying.
func captureFailure(t *testing.T) *error {
	t.Helper()
	var got error
	contextFailure = func(err error) { got = err }
	return &got
}

// writeContexts writes a config.yaml directly, so a test states the on-disk
// state it wants rather than reaching it through the commands under test.
func writeContexts(t *testing.T, current string, ctxs map[string]contextcfg.Context) {
	t.Helper()
	cfg := &contextcfg.Config{Current: current, Contexts: ctxs}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config.yaml: %v", err)
	}
}

// --- The resolution matrix ---------------------------------------------------

func TestLoad_CIShortCircuit_ReadsNoLocalState(t *testing.T) {
	// The claim under test is "CI is byte-for-byte unchanged". Asserting that
	// the right values come back would not prove it — the same values could
	// come from the config file. So the config file is made DELIBERATELY
	// MALFORMED: if anything reads it, Load reports an error and the test's
	// isolate() handler fails the test. A keyring read is caught the same way.
	for _, tc := range []struct{ name, env, value string }{
		{"DIBBLA_API_TOKEN set", "DIBBLA_API_TOKEN", "ak_from_env"},
		{"CI=true", "CI", "true"},
		{"GITHUB_ACTIONS", "GITHUB_ACTIONS", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolate(t)
			fk := newFakeKeyring(t)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
				[]byte("current: [this is not\n  valid: yaml\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			readKeyring := false
			origGet := credential.KeyringGet
			credential.KeyringGet = func(s, k string) (string, error) {
				readKeyring = true
				return origGet(s, k)
			}
			t.Cleanup(func() { credential.KeyringGet = origGet })

			t.Setenv(tc.env, tc.value)
			cfg := Load()

			if readKeyring {
				t.Error("the keyring was read; the env/CI short-circuit must return before any local store")
			}
			if fk.has("api_token::prod") {
				t.Error("a context token was written; migration must not run under the short-circuit")
			}
			if cfg.Context != "" {
				t.Errorf("Context = %q, want empty under the env/CI short-circuit", cfg.Context)
			}
			if tc.env == "DIBBLA_API_TOKEN" && cfg.APIToken != "ak_from_env" {
				t.Errorf("APIToken = %q, want the env value", cfg.APIToken)
			}
			if cfg.APIURL != DefaultAPIURL {
				t.Errorf("APIURL = %q, want the default", cfg.APIURL)
			}
		})
	}
}

func TestLoad_CIShortCircuit_DoesNotMigrate(t *testing.T) {
	// A second observable for the same claim, because the first one is inert in
	// its own scenario: with a config.yaml already present, migration returns on
	// a stat and never touches the keyring, so "no keyring read" would hold even
	// if the short-circuit were removed. Here there is NO config.yaml and there
	// IS a legacy credential — the one state in which the context layer must
	// write to disk. If the short-circuit stops working, migration runs and
	// leaves evidence.
	for _, tc := range []struct{ env, value string }{
		{"DIBBLA_API_TOKEN", "ak_from_env"},
		{"CI", "true"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			isolate(t)
			fk := newFakeKeyring(t)
			fk.items["dibbla-cli/api_token"] = "legacy-token"
			fk.items["dibbla-cli/api_url"] = "https://api.haja.fatshark.se"
			t.Setenv(tc.env, tc.value)

			cfg := Load()

			if contextcfg.Exists() {
				t.Errorf("%s was created; the env/CI path must not migrate anything", contextcfg.Path())
			}
			if fk.has("api_token::haja") {
				t.Error("a per-context token was written under the env/CI short-circuit")
			}
			// And the resolved values are the pre-context ones: env token,
			// default URL. In particular NOT the legacy stored URL, which is
			// what a leaked context layer would have supplied.
			if cfg.APIURL != DefaultAPIURL {
				t.Errorf("APIURL = %q, want the default — the stored URL must not leak into a CI run", cfg.APIURL)
			}
			if tc.env == "CI" && cfg.APIToken != "" {
				t.Errorf("APIToken = %q, want empty in CI with no DIBBLA_API_TOKEN", cfg.APIToken)
			}
		})
	}
}

func TestLoad_ContextLadder(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		envContext string
		envURL     string
		envOrg     string
		flagOrg    string
		wantCtx    string
		wantURL    string
		wantToken  string
		wantOrg    string
	}{
		{
			name: "current: in config.yaml", wantCtx: "prod",
			wantURL: "https://api.dibbla.com", wantToken: "tok-prod", wantOrg: "org-prod",
		},
		{
			name: "DIBBLA_CONTEXT beats current:", envContext: "haja", wantCtx: "haja",
			wantURL: "https://api.haja.fatshark.se", wantToken: "tok-haja", wantOrg: "org-haja",
		},
		{
			name: "--context beats DIBBLA_CONTEXT", flag: "dev", envContext: "haja", wantCtx: "dev",
			wantURL: "https://api.dibbla.net", wantToken: "tok-dev", wantOrg: "",
		},
		{
			name:   "DIBBLA_API_URL overrides the context's URL but not its token",
			envURL: "https://api.override.example", wantCtx: "prod",
			wantURL: "https://api.override.example", wantToken: "tok-prod", wantOrg: "org-prod",
		},
		{
			name: "DIBBLA_ORG_ID overrides the context's org pin", envOrg: "org-from-env",
			wantCtx: "prod", wantURL: "https://api.dibbla.com", wantToken: "tok-prod",
			wantOrg: "org-from-env",
		},
		{
			name: "--org beats DIBBLA_ORG_ID and the pin", envOrg: "org-from-env", flagOrg: "org-from-flag",
			wantCtx: "prod", wantURL: "https://api.dibbla.com", wantToken: "tok-prod",
			wantOrg: "org-from-flag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			fk := newFakeKeyring(t)
			writeContexts(t, "prod", map[string]contextcfg.Context{
				"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod", OrgName: "Prod Inc"},
				"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-haja"},
				"dev":  {APIURL: "https://api.dibbla.net"},
			})
			for name, tok := range map[string]string{"prod": "tok-prod", "haja": "tok-haja", "dev": "tok-dev"} {
				fk.items["dibbla-cli/api_token::"+name] = tok
			}

			ContextOverride = tt.flag
			FlagOrgID = tt.flagOrg
			if tt.envContext != "" {
				t.Setenv("DIBBLA_CONTEXT", tt.envContext)
			}
			if tt.envURL != "" {
				t.Setenv("DIBBLA_API_URL", tt.envURL)
			}
			if tt.envOrg != "" {
				t.Setenv("DIBBLA_ORG_ID", tt.envOrg)
			}

			cfg := Load()
			if cfg.Context != tt.wantCtx {
				t.Errorf("Context = %q, want %q", cfg.Context, tt.wantCtx)
			}
			if cfg.APIURL != tt.wantURL {
				t.Errorf("APIURL = %q, want %q", cfg.APIURL, tt.wantURL)
			}
			if cfg.APIToken != tt.wantToken {
				t.Errorf("APIToken = %q, want %q", cfg.APIToken, tt.wantToken)
			}
			if cfg.OrgID != tt.wantOrg {
				t.Errorf("OrgID = %q, want %q", cfg.OrgID, tt.wantOrg)
			}
		})
	}
}

func TestLoad_NoContextAtAll_FallsBackToDefaultWithNoToken(t *testing.T) {
	isolate(t)
	newFakeKeyring(t)

	cfg := Load()
	if cfg.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q, want the default endpoint", cfg.APIURL)
	}
	if cfg.APIToken != "" {
		t.Errorf("APIToken = %q, want empty on a machine with no login", cfg.APIToken)
	}
	if contextcfg.Exists() {
		t.Error("a fresh install must not create config.yaml until something is stored")
	}
}

func TestLoad_MalformedConfig_ErrorsAndNamesTheFile(t *testing.T) {
	dir := isolate(t)
	newFakeKeyring(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("contexts: [not, a, map]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := captureFailure(t)
	cfg := Load()

	if *got == nil {
		t.Fatal("a malformed config.yaml must be reported, not silently ignored")
	}
	if !strings.Contains((*got).Error(), path) {
		t.Errorf("the error must name the file so the user can fix it; got: %v", *got)
	}
	// The dangerous outcome is not the error — it is quietly acting as though
	// the user were logged in to production.
	if cfg.APIToken != "" {
		t.Errorf("APIToken = %q, want empty when the context list cannot be read", cfg.APIToken)
	}
}

func TestLoad_UnknownNamedContext_ErrorsRatherThanFallingBackToProd(t *testing.T) {
	for _, via := range []string{"flag", "env"} {
		t.Run(via, func(t *testing.T) {
			isolate(t)
			fk := newFakeKeyring(t)
			writeContexts(t, "prod", map[string]contextcfg.Context{
				"prod": {APIURL: "https://api.dibbla.com"},
			})
			fk.items["dibbla-cli/api_token::prod"] = "tok-prod"

			if via == "flag" {
				ContextOverride = "typo"
			} else {
				t.Setenv("DIBBLA_CONTEXT", "typo")
			}

			got := captureFailure(t)
			cfg := Load()

			if *got == nil {
				t.Fatal("naming a context that does not exist must be an error")
			}
			if !strings.Contains((*got).Error(), "typo") {
				t.Errorf("the error must name the context asked for; got: %v", *got)
			}
			// This is the assertion that matters: a mistyped --context must
			// never quietly resolve to another context's credentials.
			if cfg.APIToken != "" {
				t.Errorf("APIToken = %q, want empty — a typo must not inherit prod's token", cfg.APIToken)
			}
		})
	}
}

// --- Migration ---------------------------------------------------------------

func TestMigrate_FromKeyring_NoReLoginNeeded(t *testing.T) {
	isolate(t)
	fk := newFakeKeyring(t)
	fk.items["dibbla-cli/api_token"] = "legacy-token"
	fk.items["dibbla-cli/api_url"] = "https://api.haja.fatshark.se"
	fk.items["dibbla-cli/org_id"] = "org-legacy"
	fk.items["dibbla-cli/org_name"] = "Legacy Org"

	cfg := Load()

	if cfg.Context != "haja" {
		t.Errorf("Context = %q, want the name derived from the stored URL", cfg.Context)
	}
	if cfg.APIToken != "legacy-token" {
		t.Errorf("APIToken = %q — migration must not require a re-login", cfg.APIToken)
	}
	if cfg.APIURL != "https://api.haja.fatshark.se" {
		t.Errorf("APIURL = %q, want the stored URL", cfg.APIURL)
	}
	// Part C: the machine-wide pin becomes this context's pin. Losing it here
	// would silently switch the user to their default org.
	if cfg.OrgID != "org-legacy" || cfg.OrgName != "Legacy Org" {
		t.Errorf("org = (%q,%q), want the legacy pin carried into the context", cfg.OrgID, cfg.OrgName)
	}
	if !fk.has("api_token::haja") {
		t.Error("the per-context token must be written")
	}
	// The legacy slot is kept as a mirror rather than deleted, so a binary
	// predating contexts still authenticates after the upgrade.
	if fk.get("api_token") != "legacy-token" {
		t.Error("the legacy keyring slot must survive migration as a compatibility mirror")
	}
}

func TestMigrate_FromCredentialsFile_PreservesTheOrgPin(t *testing.T) {
	// The regression this exists for: the rescued branch's Migrate() called
	// DeleteTokenFile(), an os.Remove of the whole credentials.env — which on a
	// keyring-less host also holds DIBBLA_ORG_ID/DIBBLA_ORG_NAME. Migrating
	// would have destroyed the org pin, silently, only on the hosts least able
	// to notice.
	isolate(t)
	noKeyring(t)

	if err := credential.SetTokenFile("file-token", "https://api.dibbla.net"); err != nil {
		t.Fatal(err)
	}
	if err := credential.SetOrgFile("org-file", "File Org"); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if cfg.Context != "dibbla" {
		t.Errorf("Context = %q, want the name derived from api.dibbla.net", cfg.Context)
	}
	if cfg.APIToken != "file-token" {
		t.Errorf("APIToken = %q, want the token from the credentials file", cfg.APIToken)
	}
	if cfg.OrgID != "org-file" || cfg.OrgName != "File Org" {
		t.Errorf("org = (%q,%q), want the pin preserved through migration", cfg.OrgID, cfg.OrgName)
	}
	// The legacy file must still be readable by a pre-context binary.
	tok, url, err := credential.GetTokenFile()
	if err != nil || tok != "file-token" {
		t.Errorf("legacy credentials.env = (%q,%v), want the token still there", tok, err)
	}
	if url != "https://api.dibbla.net" {
		t.Errorf("legacy credentials.env URL = %q, want it intact", url)
	}
	id, name, _ := credential.GetOrgFile()
	if id != "org-file" || name != "File Org" {
		t.Errorf("legacy org pin = (%q,%q), want it intact", id, name)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	isolate(t)
	fk := newFakeKeyring(t)
	fk.items["dibbla-cli/api_token"] = "legacy-token"

	name1, did1, err := Migrate()
	if err != nil || !did1 || name1 != "prod" {
		t.Fatalf("first Migrate = (%q,%v,%v), want (prod,true,nil)", name1, did1, err)
	}
	before, err := os.ReadFile(contextcfg.Path())
	if err != nil {
		t.Fatal(err)
	}

	name2, did2, err := Migrate()
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if did2 {
		t.Errorf("second Migrate reported a migration (name %q); it must be a no-op", name2)
	}
	after, err := os.ReadFile(contextcfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("config.yaml changed on the second run:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMigrate_NothingToImport_WritesNoFile(t *testing.T) {
	isolate(t)
	newFakeKeyring(t)

	name, did, err := Migrate()
	if err != nil || did || name != "" {
		t.Fatalf("Migrate = (%q,%v,%v), want a no-op on a fresh install", name, did, err)
	}
	if contextcfg.Exists() {
		t.Errorf("a fresh install must not create %s", contextcfg.Path())
	}
}

func TestMigrate_TokenWriteFails_LeavesLegacyStateIntact(t *testing.T) {
	// Write-before-anything-else: if the per-context token cannot be stored,
	// config.yaml must not be written claiming a context whose token is not
	// there. Testable only because the keyring is behind a seam that can be
	// made to fail on demand.
	isolate(t)
	fk := newFakeKeyring(t)
	fk.items["dibbla-cli/api_token"] = "legacy-token"
	fk.failSet = func(key string) error {
		if strings.HasPrefix(key, "api_token::") {
			return errors.New("keyring write refused")
		}
		return nil
	}

	_, did, err := Migrate()
	if err == nil {
		t.Fatal("Migrate must report a failed token write")
	}
	if did {
		t.Error("Migrate reported success after the token write failed")
	}
	if contextcfg.Exists() {
		t.Error("config.yaml must not be written when the token could not be stored")
	}
	if fk.get("api_token") != "legacy-token" {
		t.Error("the legacy credential must be left intact when migration fails")
	}
}

// --- The legacy mirror -------------------------------------------------------

func TestSyncLegacyMirror_PointsTheLegacySlotAtTheCurrentContext(t *testing.T) {
	isolate(t)
	fk := newFakeKeyring(t)
	writeContexts(t, "prod", map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod", OrgName: "Prod"},
		"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-haja", OrgName: "Haja"},
	})
	fk.items["dibbla-cli/api_token::prod"] = "tok-prod"
	fk.items["dibbla-cli/api_token::haja"] = "tok-haja"

	SyncLegacyMirror()
	if fk.get("api_token") != "tok-prod" {
		t.Errorf("legacy token = %q, want the current context's", fk.get("api_token"))
	}
	if fk.has("api_url") {
		t.Error("the default endpoint must clear the legacy api_url, matching the old convention")
	}
	if fk.get("org_id") != "org-prod" {
		t.Errorf("legacy org = %q, want the current context's pin", fk.get("org_id"))
	}

	// Switch, and the mirror follows — this is what keeps a pre-context binary
	// pointed at the same server the user just selected.
	cfg, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Current = "haja"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	SyncLegacyMirror()
	if fk.get("api_token") != "tok-haja" {
		t.Errorf("legacy token = %q, want the newly-current context's", fk.get("api_token"))
	}
	if fk.get("api_url") != "https://api.haja.fatshark.se" {
		t.Errorf("legacy api_url = %q, want the newly-current context's", fk.get("api_url"))
	}
	if fk.get("org_id") != "org-haja" {
		t.Errorf("legacy org = %q, want the newly-current context's pin", fk.get("org_id"))
	}
}

func TestSyncLegacyMirror_NoCurrentContext_ClearsTheLegacySlot(t *testing.T) {
	isolate(t)
	fk := newFakeKeyring(t)
	fk.items["dibbla-cli/api_token"] = "stale"
	writeContexts(t, "", map[string]contextcfg.Context{})

	SyncLegacyMirror()

	if fk.has("api_token") {
		t.Error("with no current context the legacy mirror must be cleared, or the CLI keeps reporting a login that is gone")
	}
}

// --- Part C: the org is per-context, and unpinned means no header ------------

func TestResolveContext_UnpinnedContextCarriesNoOrg(t *testing.T) {
	// "Unpinned" has an observable meaning: no X-Org-ID header at all. The
	// header is attached in internal/orgctx only when cfg.OrgID is non-empty,
	// so an empty OrgID here is exactly that observable.
	isolate(t)
	fk := newFakeKeyring(t)
	writeContexts(t, "dev", map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod"},
		"dev":  {APIURL: "https://api.dibbla.net"},
	})
	fk.items["dibbla-cli/api_token::dev"] = "tok-dev"

	cfg := Load()
	if cfg.OrgID != "" {
		t.Errorf("OrgID = %q, want empty — an unpinned context must not inherit another context's org", cfg.OrgID)
	}
}

func TestResolveContext_PinsAreIndependentPerContext(t *testing.T) {
	isolate(t)
	fk := newFakeKeyring(t)
	writeContexts(t, "prod", map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com", Org: "org-A"},
		"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-B"},
	})
	fk.items["dibbla-cli/api_token::prod"] = "tok-prod"
	fk.items["dibbla-cli/api_token::haja"] = "tok-haja"

	if got := Load(); got.OrgID != "org-A" {
		t.Errorf("prod OrgID = %q, want org-A", got.OrgID)
	}
	ContextOverride = "haja"
	if got := Load(); got.OrgID != "org-B" {
		t.Errorf("haja OrgID = %q, want org-B — this is the wrong-org-to-the-wrong-server case", got.OrgID)
	}
}
