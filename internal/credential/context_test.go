package credential

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
)

// fakeKeyring replaces the three seams with an in-memory store. go-keyring's
// own MockInit is process-global with no per-test isolation and, decisively for
// this package, no way to make one operation fail — which is the only way to
// test the write-before-delete ordering these functions exist to support.
type fakeKeyring struct {
	items   map[string]string
	failSet func(key string) error
	sets    []string
	deletes []string
}

func newFakeKeyring(t *testing.T) *fakeKeyring {
	t.Helper()
	fk := &fakeKeyring{items: map[string]string{}}
	g, s, d := KeyringGet, KeyringSet, KeyringDelete
	KeyringGet = func(_, key string) (string, error) {
		v, ok := fk.items[key]
		if !ok {
			return "", keyring.ErrNotFound
		}
		return v, nil
	}
	KeyringSet = func(_, key, value string) error {
		fk.sets = append(fk.sets, key)
		if fk.failSet != nil {
			if err := fk.failSet(key); err != nil {
				return err
			}
		}
		fk.items[key] = value
		return nil
	}
	KeyringDelete = func(_, key string) error {
		fk.deletes = append(fk.deletes, key)
		if _, ok := fk.items[key]; !ok {
			return keyring.ErrNotFound
		}
		delete(fk.items, key)
		return nil
	}
	t.Cleanup(func() { KeyringGet, KeyringSet, KeyringDelete = g, s, d })
	return fk
}

func TestContextToken_KeysAreIndependentAndDoNotCollideWithLegacy(t *testing.T) {
	fk := newFakeKeyring(t)

	if err := SetContextToken("prod", "tok-prod"); err != nil {
		t.Fatal(err)
	}
	if err := SetContextToken("haja", "tok-haja"); err != nil {
		t.Fatal(err)
	}
	if err := SetToken("legacy"); err != nil {
		t.Fatal(err)
	}

	// The single-slot destruction this whole proposal exists to fix: storing a
	// second context must leave the first readable.
	for name, want := range map[string]string{"prod": "tok-prod", "haja": "tok-haja"} {
		got, err := GetContextToken(name)
		if err != nil || got != want {
			t.Errorf("GetContextToken(%q) = (%q,%v), want %q", name, got, err, want)
		}
	}
	if got, _ := GetToken(); got != "legacy" {
		t.Errorf("legacy slot = %q, want it untouched by per-context writes", got)
	}
	// A context can never be confused with the legacy key, because "::" cannot
	// occur in a valid context name.
	if _, ok := fk.items[keyToken]; !ok {
		t.Error("the legacy key must be a distinct key, not shadowed by a context")
	}

	if err := DeleteContextToken("prod"); err != nil {
		t.Fatal(err)
	}
	if got, err := GetContextToken("prod"); err != nil || got != "" {
		t.Errorf("after delete, GetContextToken(prod) = (%q,%v), want empty", got, err)
	}
	if got, _ := GetContextToken("haja"); got != "tok-haja" {
		t.Error("deleting one context's token removed another's")
	}
}

func TestDeleteContextToken_MissingIsSuccess(t *testing.T) {
	newFakeKeyring(t)
	if err := DeleteContextToken("never-existed"); err != nil {
		t.Errorf("deleting an absent token must be success, got %v", err)
	}
}

func TestGetContextToken_KeyringErrorIsNotSilentlyEmpty(t *testing.T) {
	// "Absent" and "broken" must not look the same: a caller that treats a
	// keyring failure as "no token" falls through to the file path and then
	// reports "not logged in" for what is actually a locked keyring.
	boom := errors.New("keyring is locked")
	g := KeyringGet
	KeyringGet = func(string, string) (string, error) { return "", boom }
	t.Cleanup(func() { KeyringGet = g })

	if _, err := GetContextToken("prod"); err == nil {
		t.Error("a keyring failure must be reported, not flattened to an empty token")
	}
}

func TestSetContextToken_WriteFailurePropagates(t *testing.T) {
	// The seam's reason for existing: migration and rename both write before
	// they delete, and that ordering can only be tested if a write can be made
	// to fail on demand.
	fk := newFakeKeyring(t)
	fk.failSet = func(string) error { return errors.New("refused") }
	if err := SetContextToken("prod", "tok"); err == nil {
		t.Error("a refused keyring write must surface")
	}
}

func TestContextTokenFile_PerContextAndPathSafe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))

	if err := SetContextTokenFile("prod", "tok-prod", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetContextTokenFile("haja", "tok-haja", "https://api.haja.fatshark.se"); err != nil {
		t.Fatal(err)
	}

	tok, url, err := GetContextTokenFile("haja")
	if err != nil || tok != "tok-haja" || url != "https://api.haja.fatshark.se" {
		t.Fatalf("GetContextTokenFile(haja) = (%q,%q,%v)", tok, url, err)
	}
	if tok, _, _ := GetContextTokenFile("prod"); tok != "tok-prod" {
		t.Errorf("prod token = %q, want it unaffected by the haja write", tok)
	}

	// The legacy file is a separate artefact, not one of the per-context ones.
	if _, err := os.Stat(filepath.Join(dir, credFileName)); !os.IsNotExist(err) {
		t.Error("writing per-context files must not create the legacy credentials.env by itself")
	}

	names := ListContextTokenFiles()
	if len(names) != 2 || names[0] != "haja" || names[1] != "prod" {
		t.Errorf("ListContextTokenFiles = %v, want [haja prod] sorted", names)
	}

	// The legacy credentials.env sits in the same directory and matches the
	// same prefix/suffix. It must not be enumerated as a context — `dibbla
	// uninstall` walks this list, and a context named "" is not a context.
	if err := SetTokenFile("legacy-tok", ""); err != nil {
		t.Fatal(err)
	}
	names = ListContextTokenFiles()
	for _, n := range names {
		if n == "" {
			t.Error("ListContextTokenFiles returned an empty name; the legacy credentials.env was mistaken for a context")
		}
	}
	if len(names) != 2 {
		t.Errorf("ListContextTokenFiles = %v after the legacy file appeared, want the same two contexts", names)
	}

	// A hand-edited config.yaml can name a context anything. A name with a
	// separator must not produce a 0600 file holding a bearer token outside
	// the config directory.
	for _, bad := range []string{"../../evil", "a/b", "..", ""} {
		if p := ContextTokenFilePath(bad); p != "" {
			t.Errorf("ContextTokenFilePath(%q) = %q, want empty", bad, p)
		}
		if err := SetContextTokenFile(bad, "tok", ""); err == nil {
			t.Errorf("SetContextTokenFile(%q) succeeded; it must refuse an unusable name", bad)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "credentials.") {
			t.Errorf("unexpected file %q written into the config directory", e.Name())
		}
	}
}

func TestDeleteContextTokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))

	if err := SetContextTokenFile("dev", "tok", ""); err != nil {
		t.Fatal(err)
	}
	if err := DeleteContextTokenFile("dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ContextTokenFilePath("dev")); !os.IsNotExist(err) {
		t.Error("the per-context credentials file must be gone")
	}
	if err := DeleteContextTokenFile("dev"); err != nil {
		t.Errorf("deleting an absent file must be success, got %v", err)
	}
	if names := ListContextTokenFiles(); len(names) != 0 {
		t.Errorf("ListContextTokenFiles = %v, want empty", names)
	}
}
