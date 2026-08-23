package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/credential/credtest"
)

// `dibbla status` does not call config.Load(). It re-implements the resolution
// ladder in three local resolvers, because Load returns values without saying
// where they came from and saying so is status's entire job. That duplication
// is a standing hazard: after named contexts there are several ladders in this
// codebase that have to agree, and a comment asking the next person to keep
// them in step is not a mechanism.
//
// So this test drives status and Load through IDENTICAL environments and
// requires the same answer. It is the mechanism.

func statusIsolate(t *testing.T) *credtest.Fake {
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

func TestStatusAndLoadResolveIdentically(t *testing.T) {
	type env struct {
		name       string
		flagCtx    string
		flagOrg    string
		envCtx     string
		envURL     string
		envToken   string
		envOrg     string
		ci         string
		noContexts bool
	}
	cases := []env{
		{name: "the selected context"},
		{name: "--context override", flagCtx: "haja"},
		{name: "DIBBLA_CONTEXT override", envCtx: "haja"},
		{name: "--context wins over DIBBLA_CONTEXT", flagCtx: "dev", envCtx: "haja"},
		{name: "DIBBLA_API_URL overrides the URL", envURL: "https://api.override.example"},
		{name: "DIBBLA_AUTH_SERVICE_URL is honoured too", envURL: ""},
		{name: "DIBBLA_API_TOKEN short-circuits everything", envToken: "ak_env"},
		{name: "CI short-circuits everything", ci: "true"},
		{name: "DIBBLA_ORG_ID overrides the pin", envOrg: "org-env"},
		{name: "--org wins over everything", flagOrg: "org-flag", envOrg: "org-env"},
		{name: "an unpinned context", flagCtx: "dev"},
		{name: "no contexts at all", noContexts: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := statusIsolate(t)
			if !c.noContexts {
				cfg := &contextcfg.Config{
					Current: "prod",
					Contexts: map[string]contextcfg.Context{
						"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod", OrgName: "Prod"},
						"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-haja"},
						"dev":  {APIURL: "https://api.dibbla.net"},
					},
				}
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				for n, tok := range map[string]string{"prod": "tok-prod", "haja": "tok-haja", "dev": "tok-dev"} {
					fake.Items[credtest.ContextToken(n)] = tok
				}
			}

			config.ContextOverride, config.FlagOrgID = c.flagCtx, c.flagOrg
			if c.envCtx != "" {
				t.Setenv("DIBBLA_CONTEXT", c.envCtx)
			}
			if c.envURL != "" {
				t.Setenv("DIBBLA_API_URL", c.envURL)
			}
			if c.envToken != "" {
				t.Setenv("DIBBLA_API_TOKEN", c.envToken)
			}
			if c.envOrg != "" {
				t.Setenv("DIBBLA_ORG_ID", c.envOrg)
			}
			if c.ci != "" {
				t.Setenv("CI", c.ci)
			}

			// --no-validate: this is about resolution, not about the network.
			report := buildStatusReport(true)
			loaded := config.Load()

			if report.APIURL != loaded.APIURL {
				t.Errorf("API URL: status says %q, config.Load says %q — the two ladders have drifted",
					report.APIURL, loaded.APIURL)
			}
			if report.TokenConfigured != (loaded.APIToken != "") {
				t.Errorf("token presence: status says %v, config.Load says %v",
					report.TokenConfigured, loaded.APIToken != "")
			}
			if report.OrgID != loaded.OrgID {
				t.Errorf("org: status says %q, config.Load says %q", report.OrgID, loaded.OrgID)
			}
			if report.Context != loaded.Context {
				t.Errorf("context: status says %q, config.Load says %q", report.Context, loaded.Context)
			}
		})
	}
}

func TestStatus_ReportsTheContextAndHowManyExist(t *testing.T) {
	fake := statusIsolate(t)
	cfg := &contextcfg.Config{
		Current: "haja",
		Contexts: map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("haja")] = "tok-haja"

	r := buildStatusReport(true)
	if r.Context != "haja" {
		t.Errorf("Context = %q, want haja", r.Context)
	}
	if r.ContextCount != 2 {
		t.Errorf("ContextCount = %d, want 2 — the count is what tells a user there is somewhere else to switch to", r.ContextCount)
	}
	if r.APIURL != "https://api.haja.fatshark.se" {
		t.Errorf("APIURL = %q, want the active context's", r.APIURL)
	}
}

func TestStatus_UnderCIReportsNoContext(t *testing.T) {
	// Reporting a stored context in CI would describe state the run will never
	// read, which is the misleading half of a status command.
	fake := statusIsolate(t)
	cfg := &contextcfg.Config{Current: "prod", Contexts: map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("prod")] = "tok-prod"
	t.Setenv("CI", "true")

	r := buildStatusReport(true)
	if r.Context != "" {
		t.Errorf("Context = %q in CI, want empty", r.Context)
	}
	if r.TokenConfigured {
		t.Error("a stored context token must not be reported as configured in CI")
	}
	if r.APIURL != config.DefaultAPIURL {
		t.Errorf("APIURL = %q in CI, want the default", r.APIURL)
	}
}

// --- login and logout, the single-slot destruction this proposal removes -----

func TestLogin_SecondServerLeavesTheFirstIntact(t *testing.T) {
	fake := statusIsolate(t)

	if _, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok-prod"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := storeLoginAsContext("https://api.haja.fatshark.se", "tok-haja"); err != nil {
		t.Fatal(err)
	}

	// The whole point of P-0011, asserted directly.
	if tok, _ := credential.GetContextToken("prod"); tok != "tok-prod" {
		t.Errorf("prod's token = %q after logging in elsewhere — this is the destruction the proposal exists to remove", tok)
	}
	if tok, _ := credential.GetContextToken("haja"); tok != "tok-haja" {
		t.Errorf("haja's token = %q", tok)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Contexts) != 2 {
		t.Errorf("got %d contexts, want 2: %+v", len(store.Contexts), store.Contexts)
	}
	if store.Current != "haja" {
		t.Errorf("current = %q, want the newest login", store.Current)
	}
	if fake.Get("api_token") != "tok-haja" {
		t.Errorf("legacy mirror = %q, want the newest login's token", fake.Get("api_token"))
	}
}

func TestLogin_SameURLRefreshesRatherThanDuplicating(t *testing.T) {
	statusIsolate(t)

	name1, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok-v1")
	if err != nil {
		t.Fatal(err)
	}
	// Trailing slash and all: the same server is the same server.
	name2, _, _, err := storeLoginAsContext("https://api.dibbla.com/", "tok-v2")
	if err != nil {
		t.Fatal(err)
	}
	if name1 != name2 {
		t.Errorf("the same URL produced two contexts (%q, %q); logging in twice must refresh", name1, name2)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Contexts) != 1 {
		t.Errorf("got %d contexts, want 1: %+v", len(store.Contexts), store.Contexts)
	}
	if tok, _ := credential.GetContextToken(name1); tok != "tok-v2" {
		t.Errorf("token = %q, want the refreshed one", tok)
	}
}

func TestLogin_RefreshKeepsTheOrgPin_ButRepointingDropsIt(t *testing.T) {
	statusIsolate(t)

	name, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok-v1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := store.Get(name)
	ctx.Org, ctx.OrgName = "org-1", "Org One"
	store.Set(name, ctx)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	// Re-authenticating against the same server is not a request to change org.
	if _, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok-v2"); err != nil {
		t.Fatal(err)
	}
	store, _ = contextcfg.Load()
	if got, _ := store.Get(name); got.Org != "org-1" {
		t.Errorf("org pin = %q after a refresh, want it kept", got.Org)
	}

	// Pointing that same context at a DIFFERENT server must drop it: an org id
	// from the old server means nothing on the new one.
	loginContext = name
	t.Cleanup(func() { loginContext = "" })
	if _, _, _, err := storeLoginAsContext("https://api.other.example", "tok-v3"); err != nil {
		t.Fatal(err)
	}
	store, _ = contextcfg.Load()
	got, _ := store.Get(name)
	if got.Org != "" {
		t.Errorf("org pin = %q after repointing the context at another server, want it dropped", got.Org)
	}
	if got.APIURL != "https://api.other.example" {
		t.Errorf("APIURL = %q, want the new server", got.APIURL)
	}
}

func TestLogin_NoSwitchLeavesTheSelectionAlone(t *testing.T) {
	statusIsolate(t)

	if _, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok-prod"); err != nil {
		t.Fatal(err)
	}
	loginNoSwitch = true
	t.Cleanup(func() { loginNoSwitch = false })
	name, _, switched, err := storeLoginAsContext("https://api.haja.fatshark.se", "tok-haja")
	if err != nil {
		t.Fatal(err)
	}
	if switched {
		t.Error("--no-switch reported a switch")
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if store.Current != "prod" {
		t.Errorf("current = %q, want prod — --no-switch must not move it", store.Current)
	}
	if tok, _ := credential.GetContextToken(name); tok != "tok-haja" {
		t.Errorf("the login was still meant to be stored; token = %q", tok)
	}
}

func TestLogin_RefusesAnUnusableContextName(t *testing.T) {
	statusIsolate(t)
	loginContext = "../../evil"
	t.Cleanup(func() { loginContext = "" })

	if _, _, _, err := storeLoginAsContext("https://api.dibbla.com", "tok"); err == nil {
		t.Fatal("an unusable --context name must be refused: it becomes a filename holding a bearer token")
	}
	if contextcfg.Exists() {
		t.Error("a refused login must not write config.yaml")
	}
}

func TestResolveLoginBaseURL_PrefersTheActiveContextOverProduction(t *testing.T) {
	// The sharp edge this fixes predates contexts: a bare `dibbla login` while
	// working against a customer instance fell through to production, silently
	// re-targeting the user — and, before contexts, destroying the credential
	// they were using.
	fake := statusIsolate(t)
	cfg := &contextcfg.Config{Current: "haja", Contexts: map[string]contextcfg.Context{
		"haja": {APIURL: "https://api.haja.fatshark.se"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("haja")] = "tok-haja"

	got, err := resolveLoginBaseURL(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.haja.fatshark.se" {
		t.Errorf("a bare `dibbla login` resolved to %q, want the context in use", got)
	}

	// An explicit target still wins — this only decides what "no target" means.
	loginAPIURL = "https://api.dibbla.net"
	t.Cleanup(func() { loginAPIURL = "" })
	if got, _ := resolveLoginBaseURL(nil); got != "https://api.dibbla.net" {
		t.Errorf("--api-url resolved to %q, want the explicit target", got)
	}
}

func TestApiKeysURL_PointsAtTheInstanceBeingLoggedInTo(t *testing.T) {
	// Telling someone logging in to a customer instance to mint their token at
	// app.dibbla.com sends them to a different company's product to create a
	// credential that would not work.
	if got := apiKeysURLFor(config.DefaultAPIURL); got != "https://app.dibbla.com/api-keys" {
		t.Errorf("default endpoint = %q", got)
	}
	if got := apiKeysURLFor("https://api.haja.fatshark.se"); got != "https://app.haja.fatshark.se/api-keys" {
		t.Errorf("customer instance = %q, want the instance's own portal", got)
	}
	// A host whose app URL cannot be derived gets no guess: a wrong URL is
	// worse than none.
	if got := apiKeysURLFor("https://dibbla.example.com"); got != "" {
		t.Errorf("non-derivable host = %q, want empty rather than a guess", got)
	}
	if got := mintTokenAt("https://dibbla.example.com"); got == "" {
		t.Error("the prose must still say something useful when there is no URL")
	}
}

// --- logout ------------------------------------------------------------------

func runLogoutCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "dibbla"}
	root.SilenceUsage, root.SilenceErrors = true, true
	logoutContext, logoutAll = "", false
	t.Cleanup(func() { logoutContext, logoutAll = "", false })
	root.AddCommand(logoutCmd)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"logout"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func seedTwoLogins(t *testing.T, fake *credtest.Fake) {
	t.Helper()
	cfg := &contextcfg.Config{Current: "prod", Contexts: map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod"},
		"haja": {APIURL: "https://api.haja.fatshark.se"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("prod")] = "tok-prod"
	fake.Items[credtest.ContextToken("haja")] = "tok-haja"
	config.SyncLegacyMirror()
}

func TestLogout_RemovesOnlyTheContextInUse(t *testing.T) {
	fake := statusIsolate(t)
	seedTwoLogins(t, fake)

	out, err := runLogoutCmd(t)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "" {
		t.Errorf("the context in use still has a token: %q", tok)
	}
	// Logging out of production must not log you out of a customer instance.
	if tok, _ := credential.GetContextToken("haja"); tok != "tok-haja" {
		t.Errorf("haja's token = %q, want it untouched", tok)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("prod"); ok {
		t.Error("the logged-out context is still listed")
	}
	if _, ok := store.Get("haja"); !ok {
		t.Error("the other context was removed too")
	}
	if !strings.Contains(out, "haja") {
		t.Errorf("logout should say what you are still logged in to; got:\n%s", out)
	}
}

func TestLogout_NamedContext(t *testing.T) {
	fake := statusIsolate(t)
	seedTwoLogins(t, fake)

	if _, err := runLogoutCmd(t, "--context", "haja"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := credential.GetContextToken("haja"); tok != "" {
		t.Errorf("haja's token survived: %q", tok)
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "tok-prod" {
		t.Errorf("prod's token = %q, want it untouched", tok)
	}
	store, _ := contextcfg.Load()
	if store.Current != "prod" {
		t.Errorf("current = %q, want prod — logging out of another context must not move it", store.Current)
	}
}

func TestLogout_UnknownContextIsAnError(t *testing.T) {
	fake := statusIsolate(t)
	seedTwoLogins(t, fake)

	if _, err := runLogoutCmd(t, "--context", "nope"); err == nil {
		t.Fatal("logging out of a context that does not exist must fail rather than silently succeed")
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "tok-prod" {
		t.Error("a failed logout removed a token")
	}
}

func TestLogout_AllLeavesNothingBehind(t *testing.T) {
	fake := statusIsolate(t)
	seedTwoLogins(t, fake)
	if err := credential.SetContextTokenFile("haja", "tok-haja", ""); err != nil {
		t.Fatal(err)
	}
	// A credentials file with no config.yaml entry — the state a crash between
	// the two writes leaves behind. --all must mean nothing is left.
	if err := credential.SetContextTokenFile("orphan", "tok-orphan", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := runLogoutCmd(t, "--all"); err != nil {
		t.Fatalf("logout --all: %v", err)
	}
	for _, name := range []string{"prod", "haja"} {
		if tok, _ := credential.GetContextToken(name); tok != "" {
			t.Errorf("%s still has a token: %q", name, tok)
		}
	}
	if contextcfg.Exists() {
		t.Error("config.yaml survived --all")
	}
	if left := credential.ListContextTokenFiles(); len(left) != 0 {
		t.Errorf("credentials files left behind: %v", left)
	}
	if fake.Has("api_token") {
		t.Error("the legacy mirror survived --all, so the CLI still reports a login")
	}
}

func TestLogout_WithNothingConfiguredIsNotAnError(t *testing.T) {
	// "Log me out" on a machine that is already logged out has succeeded.
	statusIsolate(t)
	if _, err := runLogoutCmd(t); err != nil {
		t.Errorf("logout with nothing configured should succeed, got %v", err)
	}
}

func TestLogout_TokenRemovalFailureIsNotSwallowed(t *testing.T) {
	// Reporting a logout that did not actually remove the credential is worse
	// than failing: the user walks away believing the token is gone.
	fake := statusIsolate(t)
	seedTwoLogins(t, fake)
	fake.FailDelete = func(key string) error {
		if key == credtest.ContextToken("prod") {
			return errors.New("keyring delete refused")
		}
		return nil
	}

	if _, err := runLogoutCmd(t); err == nil {
		t.Fatal("a keyring delete that failed must surface")
	}
	store, _ := contextcfg.Load()
	if _, ok := store.Get("prod"); !ok {
		t.Error("config.yaml dropped the context while its token is still stored — an orphaned secret")
	}
}
