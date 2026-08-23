package contextcmd

import (
	"bytes"
	"encoding/json"
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

// isolate gives a test its own config dir, its own credential store, a cleared
// environment and reset package state. The command group is registered onto a
// throwaway root so each test drives real cobra dispatch rather than calling
// the run functions directly — flags, argument counts and aliases are part of
// what is under test.
func isolate(t *testing.T) (*cobra.Command, *credtest.Fake, *bytes.Buffer) {
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

	origOverride := config.ContextOverride
	config.ContextOverride = ""
	t.Cleanup(func() { config.ContextOverride = origOverride })

	root := &cobra.Command{Use: "dibbla"}
	Register(root)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SilenceUsage = true
	root.SilenceErrors = true
	return root, fake, &buf
}

func run(t *testing.T, root *cobra.Command, args ...string) error {
	t.Helper()
	root.SetArgs(args)
	return root.Execute()
}

// seed writes a config.yaml and the matching keyring tokens.
func seed(t *testing.T, fake *credtest.Fake, current string, ctxs map[string]contextcfg.Context, tokens map[string]string) {
	t.Helper()
	cfg := &contextcfg.Config{Current: current, Contexts: ctxs}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	for name, tok := range tokens {
		fake.Items[credtest.ContextToken(name)] = tok
	}
}

func readConfig(t *testing.T) *contextcfg.Config {
	t.Helper()
	cfg, err := contextcfg.Load()
	if err != nil {
		t.Fatalf("config.yaml: %v", err)
	}
	return cfg
}

// --- list --------------------------------------------------------------------

func TestList_JSONIsParseableAndMarksTheActiveContext(t *testing.T) {
	root, fake, buf := isolate(t)
	seed(t, fake, "haja",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod", OrgName: "Prod"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
		map[string]string{"prod": "tok-prod", "haja": "tok-haja"})

	if err := run(t, root, "context", "list", "--json"); err != nil {
		t.Fatalf("list --json: %v", err)
	}

	var got []contextRow
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %s", len(got), buf.String())
	}
	// Sorted, so a script can rely on the order.
	if got[0].Name != "haja" || got[1].Name != "prod" {
		t.Errorf("rows are not sorted by name: %+v", got)
	}
	if !got[0].Current || got[1].Current {
		t.Errorf("the active context is not the one marked current: %+v", got)
	}
	if !got[0].LoggedIn || !got[1].LoggedIn {
		t.Errorf("both contexts have tokens but logged_in is false: %+v", got)
	}
	if got[1].Org != "org-prod" || got[1].OrgName != "Prod" {
		t.Errorf("the org pin is missing from the JSON: %+v", got[1])
	}
}

func TestList_JSONIsAnEmptyArrayNotNull(t *testing.T) {
	// `dibbla context list --json | jq '.[]'` must work on a fresh machine.
	root, _, buf := isolate(t)
	if err := run(t, root, "context", "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("output = %q, want []", strings.TrimSpace(buf.String()))
	}
}

func TestList_ReportsAContextWithNoStoredToken(t *testing.T) {
	// A context whose token was removed still exists in config.yaml. A list
	// that showed it identically to a working one would report a login that
	// is not there.
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{"prod": {APIURL: "https://api.dibbla.com"}},
		nil)

	if err := run(t, root, "context", "list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no token stored") {
		t.Errorf("a tokenless context must be flagged; got:\n%s", buf.String())
	}
}

func TestList_HonoursTheContextOverrideWhenMarkingCurrent(t *testing.T) {
	// `dibbla context list --context haja` answers "what would the next
	// command use", which is the question worth answering.
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
		map[string]string{"prod": "t1", "haja": "t2"})

	config.ContextOverride = "haja"
	if err := run(t, root, "context", "list", "--json"); err != nil {
		t.Fatal(err)
	}
	var got []contextRow
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Current != (r.Name == "haja") {
			t.Errorf("--context haja not reflected in the listing: %+v", got)
			break
		}
	}
	// And it must not have changed anything on disk.
	if readConfig(t).Current != "prod" {
		t.Error("a --context override must not rewrite current: in config.yaml")
	}
}

// --- use ---------------------------------------------------------------------

func TestUse_SwitchesAndRepointsTheLegacyMirror(t *testing.T) {
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com", Org: "org-prod"},
			"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-haja"},
		},
		map[string]string{"prod": "tok-prod", "haja": "tok-haja"})

	if err := run(t, root, "context", "use", "haja"); err != nil {
		t.Fatalf("use: %v", err)
	}
	if got := readConfig(t).Current; got != "haja" {
		t.Errorf("current = %q, want haja", got)
	}
	// The compatibility mirror follows, or a pre-context binary keeps talking
	// to the previous server while the user believes they switched.
	if fake.Get("api_token") != "tok-haja" {
		t.Errorf("legacy token = %q, want tok-haja", fake.Get("api_token"))
	}
	if fake.Get("api_url") != "https://api.haja.fatshark.se" {
		t.Errorf("legacy api_url = %q, want the new context's", fake.Get("api_url"))
	}
	if fake.Get("org_id") != "org-haja" {
		t.Errorf("legacy org = %q, want the new context's pin", fake.Get("org_id"))
	}
	if !strings.Contains(buf.String(), "haja") {
		t.Errorf("the confirmation should name the context; got %q", buf.String())
	}
}

func TestUse_UnknownContext_ErrorsAndListsWhatExists(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{"prod": {APIURL: "https://api.dibbla.com"}},
		map[string]string{"prod": "tok"})

	err := run(t, root, "context", "use", "nope")
	if err == nil {
		t.Fatal("using an unknown context must fail")
	}
	if !strings.Contains(err.Error(), "prod") {
		t.Errorf("the error should say what does exist; got: %v", err)
	}
	if readConfig(t).Current != "prod" {
		t.Error("a failed use must not change current:")
	}
}

func TestUse_WarnsWhenTheEnvironmentShadowsTheChoice(t *testing.T) {
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
		map[string]string{"prod": "t1", "haja": "t2"})
	t.Setenv("DIBBLA_CONTEXT", "prod")

	if err := run(t, root, "context", "use", "haja"); err != nil {
		t.Fatal(err)
	}
	// Warn at the moment of the change, not at the next confusing result.
	if !strings.Contains(buf.String(), "DIBBLA_CONTEXT") {
		t.Errorf("switching under a shadowing DIBBLA_CONTEXT must warn; got:\n%s", buf.String())
	}
}

// --- current -----------------------------------------------------------------

func TestCurrent_PrintsTheNameAndRespectsTheOverride(t *testing.T) {
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"haja": {APIURL: "https://api.haja.fatshark.se"},
		},
		map[string]string{"prod": "t1", "haja": "t2"})

	if err := run(t, root, "context", "current"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "prod" {
		t.Errorf("current = %q, want prod", strings.TrimSpace(buf.String()))
	}

	buf.Reset()
	t.Setenv("DIBBLA_CONTEXT", "haja")
	if err := run(t, root, "context", "current"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "haja" {
		t.Errorf("current under DIBBLA_CONTEXT = %q, want haja", strings.TrimSpace(buf.String()))
	}
}

func TestCurrent_NoContext_IsAnError(t *testing.T) {
	root, _, _ := isolate(t)
	if err := run(t, root, "context", "current"); err == nil {
		t.Fatal("current must exit non-zero when nothing is selected, so scripts can branch on it")
	}
}

// --- rename ------------------------------------------------------------------

func TestRename_MovesTheTokenAndUpdatesCurrent(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "haja",
		map[string]contextcfg.Context{"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-1"}},
		map[string]string{"haja": "tok-haja"})

	if err := run(t, root, "context", "rename", "haja", "fatshark"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	cfg := readConfig(t)
	if _, ok := cfg.Get("haja"); ok {
		t.Error("the old name still exists")
	}
	got, ok := cfg.Get("fatshark")
	if !ok || got.APIURL != "https://api.haja.fatshark.se" || got.Org != "org-1" {
		t.Errorf("the renamed context lost data: %+v", got)
	}
	if cfg.Current != "fatshark" {
		t.Errorf("current = %q, want fatshark — renaming the active context must follow it", cfg.Current)
	}
	if tok, _ := credential.GetContextToken("fatshark"); tok != "tok-haja" {
		t.Errorf("token under the new name = %q, want tok-haja", tok)
	}
	if tok, _ := credential.GetContextToken("haja"); tok != "" {
		t.Errorf("token still readable under the old name: %q", tok)
	}
}

func TestRename_WritesBeforeDeleting_TokenSurvivesAFailedDelete(t *testing.T) {
	// The ordering guarantee, tested the only way an ordering guarantee can
	// be: by interrupting it. If the delete were to happen first, a failure
	// here would lose the token entirely.
	root, fake, _ := isolate(t)
	seed(t, fake, "haja",
		map[string]contextcfg.Context{"haja": {APIURL: "https://api.haja.fatshark.se"}},
		map[string]string{"haja": "tok-haja"})
	fake.FailDelete = func(key string) error {
		if key == credtest.ContextToken("haja") {
			return errors.New("keyring delete refused")
		}
		return nil
	}

	err := run(t, root, "context", "rename", "haja", "fatshark")
	if err == nil {
		t.Fatal("a failed re-key must be reported rather than half-applied silently")
	}
	// The token must be recoverable. Readable under BOTH names is the
	// acceptable partial state; readable under neither is not.
	if tok, _ := credential.GetContextToken("fatshark"); tok != "tok-haja" {
		t.Errorf("token under the new name = %q — the write must precede the delete", tok)
	}
	if tok, _ := credential.GetContextToken("haja"); tok != "tok-haja" {
		t.Errorf("token under the old name = %q — a failed delete must leave it in place", tok)
	}
	// And the sequence itself, not just the end state.
	var sawSet, sawDelete bool
	for _, k := range fake.Sets {
		if k == credtest.ContextToken("fatshark") {
			sawSet = true
		}
	}
	for _, k := range fake.Deletes {
		if k == credtest.ContextToken("haja") {
			sawDelete = true
		}
	}
	if !sawSet || !sawDelete {
		t.Errorf("expected a write to the new key then a delete of the old; sets=%v deletes=%v", fake.Sets, fake.Deletes)
	}
	// config.yaml must not claim a rename that did not complete.
	if _, ok := readConfig(t).Get("haja"); !ok {
		t.Error("config.yaml was rewritten despite the re-key failing")
	}
}

func TestRename_RefusesAnUnusableNewName(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{"prod": {APIURL: "https://api.dibbla.com"}},
		map[string]string{"prod": "tok"})

	for _, bad := range []string{"../../evil", "a/b", "with space"} {
		if err := run(t, root, "context", "rename", "prod", bad); err == nil {
			t.Errorf("rename to %q must be refused — the name becomes a filename and a keyring key", bad)
		}
	}
	if _, ok := readConfig(t).Get("prod"); !ok {
		t.Error("a refused rename must leave the original context alone")
	}
}

func TestRename_RefusesToOverwriteAnExistingContext(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"dev":  {APIURL: "https://api.dibbla.net"},
		},
		map[string]string{"prod": "tok-prod", "dev": "tok-dev"})

	if err := run(t, root, "context", "rename", "prod", "dev"); err == nil {
		t.Fatal("renaming onto an existing context must be refused, not silently merged")
	}
	if tok, _ := credential.GetContextToken("dev"); tok != "tok-dev" {
		t.Errorf("dev's token was clobbered: %q", tok)
	}
}

// --- rm ----------------------------------------------------------------------

func TestRm_RefusesTheCurrentContextWithoutForce_AndChangesNothing(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{"prod": {APIURL: "https://api.dibbla.com"}},
		map[string]string{"prod": "tok-prod"})

	err := run(t, root, "context", "rm", "prod")
	if err == nil {
		t.Fatal("removing the context in use must be refused without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the error should name the way out; got: %v", err)
	}
	// Nothing on disk may have moved.
	cfg := readConfig(t)
	if _, ok := cfg.Get("prod"); !ok || cfg.Current != "prod" {
		t.Errorf("a refused rm changed config.yaml: %+v", cfg)
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "tok-prod" {
		t.Errorf("a refused rm removed the token: %q", tok)
	}
}

func TestRm_ForceRemovesTheTokenAndClearsTheLegacyMirror(t *testing.T) {
	root, fake, buf := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{"prod": {APIURL: "https://api.dibbla.com"}},
		map[string]string{"prod": "tok-prod"})
	config.SyncLegacyMirror()
	if fake.Get("api_token") == "" {
		t.Fatal("precondition: the legacy mirror should hold the current context's token")
	}

	if err := run(t, root, "context", "rm", "prod", "--force"); err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	cfg := readConfig(t)
	if _, ok := cfg.Get("prod"); ok {
		t.Error("the context is still in config.yaml")
	}
	if cfg.Current != "" {
		t.Errorf("current = %q, want empty after removing the only context", cfg.Current)
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "" {
		t.Errorf("the stored token survived the removal: %q", tok)
	}
	// Leaving the mirror behind would keep the CLI, and an older binary,
	// reporting a login that no longer exists.
	if fake.Has("api_token") {
		t.Error("the legacy mirror must be cleared when the last context goes")
	}
	if !strings.Contains(buf.String(), "No context selected") {
		t.Errorf("removing the last context should say what happens next; got:\n%s", buf.String())
	}
}

func TestRm_ANonCurrentContextNeedsNoForce(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"old":  {APIURL: "https://api.old.example"},
		},
		map[string]string{"prod": "tok-prod", "old": "tok-old"})

	if err := run(t, root, "context", "rm", "old"); err != nil {
		t.Fatalf("rm of a non-current context: %v", err)
	}
	cfg := readConfig(t)
	if cfg.Current != "prod" {
		t.Errorf("current = %q, want prod — removing another context must not disturb it", cfg.Current)
	}
	if tok, _ := credential.GetContextToken("prod"); tok != "tok-prod" {
		t.Errorf("prod's token was removed: %q", tok)
	}
}

func TestRm_AlsoRemovesThePerContextCredentialsFile(t *testing.T) {
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"lab":  {APIURL: "http://127.0.0.1:9999"},
		},
		map[string]string{"prod": "tok-prod"})
	if err := credential.SetContextTokenFile("lab", "tok-lab", "http://127.0.0.1:9999"); err != nil {
		t.Fatal(err)
	}

	if err := run(t, root, "context", "rm", "lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credential.ContextTokenFilePath("lab")); !os.IsNotExist(err) {
		t.Error("the per-context credentials file must be removed with the context, or a token is left on disk unreachable")
	}
}

func TestRm_TokenRemovalFails_LeavesTheContextInPlaceRatherThanOrphaningTheSecret(t *testing.T) {
	// Ordering, again, and for the same kind of reason as rename's. If
	// config.yaml were rewritten first and the keyring delete then failed, the
	// token would still be stored with nothing referring to it: an orphaned
	// secret no dibbla command can reach or remove, on a machine whose owner
	// has just been told the context was removed.
	root, fake, _ := isolate(t)
	seed(t, fake, "prod",
		map[string]contextcfg.Context{
			"prod": {APIURL: "https://api.dibbla.com"},
			"old":  {APIURL: "https://api.old.example"},
		},
		map[string]string{"prod": "tok-prod", "old": "tok-old"})
	fake.FailDelete = func(key string) error {
		if key == credtest.ContextToken("old") {
			return errors.New("keyring delete refused")
		}
		return nil
	}

	if err := run(t, root, "context", "rm", "old"); err == nil {
		t.Fatal("rm must report a token that could not be removed")
	}
	if _, ok := readConfig(t).Get("old"); !ok {
		t.Error("config.yaml no longer lists the context while its token is still stored — the secret is now orphaned")
	}
}
