package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// These tests describe a headless Linux server: no session bus, therefore no
// keyring, therefore the file store. That host is the one the fallback was
// built for and the one it did not work on — `dibbla login` exited 1 with
// "token validated but could not be stored", because the fallback only fired
// for an error string the host does not produce.
//
// The keyring is switched off through credential.SetKeyringUsableForTest,
// which is the same switch the production probe throws, rather than by feeding
// a particular error text in. That is the point of the rewrite: the decision no
// longer depends on what another library's error happens to say.

func headlessLogin(t *testing.T) {
	t.Helper()
	statusIsolate(t)                                     // config dir, env, and the keyring fake
	t.Cleanup(credential.SetKeyringUsableForTest(false)) // ...which this then switches off
	t.Setenv(credential.EnvStore, "")
	os.Unsetenv(credential.EnvStore)
}

func TestLogin_HeadlessHostStoresToFileAndSaysSo(t *testing.T) {
	headlessLogin(t)

	res, err := storeLoginAsContext("https://api.dibbla.com", "ak_headless", "")
	if err != nil {
		t.Fatalf("login on a keyring-less host must succeed, not fail: %v", err)
	}
	if res.Store != credential.StoreFile {
		t.Errorf("store = %q, want %q", res.Store, credential.StoreFile)
	}
	if res.FallbackReason == nil {
		t.Error("the fallback must carry the reason it fired; the message used to assert one instead")
	}

	// The token is really there, and really readable back.
	tok, _, err := credential.GetContextTokenFile(res.Context)
	if err != nil || tok != "ak_headless" {
		t.Errorf("credentials file holds (%q, %v), want the token", tok, err)
	}
}

// The hint is what stops every subsequent command re-probing for a keyring
// that was already established not to exist.
func TestLogin_HeadlessHostRecordsTheStoreOnTheContext(t *testing.T) {
	headlessLogin(t)

	res, err := storeLoginAsContext("https://api.dibbla.com", "ak_headless", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, ok := store.Get(res.Context)
	if !ok {
		t.Fatalf("context %q was not saved", res.Context)
	}
	if ctx.Store != string(credential.StoreFile) {
		t.Errorf("recorded store = %q, want %q", ctx.Store, credential.StoreFile)
	}

	// config.yaml holds the hint and still holds no secret.
	body, err := os.ReadFile(contextcfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "store: file") {
		t.Errorf("config.yaml does not record the store:\n%s", body)
	}
	if strings.Contains(string(body), "ak_headless") {
		t.Fatalf("config.yaml contains the token; it must never:\n%s", body)
	}
}

// A context recorded as file-backed must not consult the keyring on read. This
// is the per-command cost the hint exists to remove — on a host with dbus-x11
// but no secret service, that lookup forks a dbus-daemon before failing.
func TestResolve_FileBackedContextSkipsTheKeyring(t *testing.T) {
	statusIsolate(t)

	// A keyring that is available but must not be consulted for this context.
	consulted := false
	g := credential.KeyringGet
	credential.KeyringGet = func(_, key string) (string, error) {
		if strings.HasPrefix(key, "api_token::") {
			consulted = true
		}
		return g("", key)
	}
	t.Cleanup(func() { credential.KeyringGet = g })

	if err := credential.SetContextTokenFile("srv", "ak_from_file", "https://api.dibbla.com"); err != nil {
		t.Fatal(err)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Current = "srv"
	store.Set("srv", contextcfg.Context{
		APIURL: "https://api.dibbla.com",
		Store:  string(credential.StoreFile),
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	r := config.ResolveContext()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.Token != "ak_from_file" {
		t.Errorf("token = %q, want the one in the credentials file", r.Token)
	}
	if consulted {
		t.Error("a context recorded as file-backed still asked the keyring for its token")
	}
}

// The hint is an optimisation, never an authority: it may only skip a read
// that would have failed. A context whose hint says "keyring" but whose token
// is in the file must still resolve, or a hint gone stale would lock a user out.
func TestResolve_KeyringHintStillFallsBackToTheFile(t *testing.T) {
	statusIsolate(t)

	if err := credential.SetContextTokenFile("srv", "ak_from_file", "https://api.dibbla.com"); err != nil {
		t.Fatal(err)
	}
	store, err := contextcfg.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Current = "srv"
	store.Set("srv", contextcfg.Context{
		APIURL: "https://api.dibbla.com",
		Store:  string(credential.StoreKeyring), // stale: nothing is in the keyring
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	r := config.ResolveContext()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.Token != "ak_from_file" {
		t.Errorf("token = %q — a stale store hint must cost a wasted lookup, never a missed token", r.Token)
	}
}

// DIBBLA_CREDENTIAL_STORE=file is the escape hatch for a host where the probe
// guesses wrong: a keyring is reachable, and the user wants the file anyway.
func TestLogin_StoreOverrideForcesTheFile(t *testing.T) {
	statusIsolate(t) // installs a working keyring fake
	t.Setenv(credential.EnvStore, "file")
	credential.ResetKeyringProbeForTest()
	t.Cleanup(credential.ResetKeyringProbeForTest)

	res, err := storeLoginAsContext("https://api.dibbla.com", "ak_forced", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Store != credential.StoreFile {
		t.Errorf("store = %q, want %q — the override must beat a working keyring", res.Store, credential.StoreFile)
	}
	tok, _, _ := credential.GetContextTokenFile(res.Context)
	if tok != "ak_forced" {
		t.Errorf("credentials file holds %q, want the token", tok)
	}
}
