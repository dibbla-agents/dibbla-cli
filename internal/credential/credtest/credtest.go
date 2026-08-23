// Package credtest provides an in-memory stand-in for the OS credential store,
// for tests in packages that read or write credentials.
//
// It exists because go-keyring's own MockInit is process-global, offers no
// per-test isolation, and — decisively — has no way to make a single operation
// fail. Several guarantees in the named-contexts work are about ORDERING: a
// token is written under the new key before the old one is deleted, in both
// migration and `dibbla context rename`. An ordering guarantee can only be
// tested by interrupting it, so the fake has to be able to refuse a write or a
// delete on demand.
//
// internal/credential's own tests keep a local copy of this rather than
// importing it, because that would be an import cycle.
package credtest

import (
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// Fake is an in-memory credential store.
type Fake struct {
	Items map[string]string

	// FailSet and FailDelete, when non-nil, decide per key whether that
	// operation should fail. Consulted per key so a test can let the write
	// that must happen first succeed and fail the one that must happen second.
	FailSet    func(key string) error
	FailDelete func(key string) error

	// Sets and Deletes record the key of every operation, in order, so a test
	// can assert the sequence and not only the end state.
	Sets    []string
	Deletes []string
}

// Install points the credential package's keyring seams at a fresh Fake, and
// the CLI's config directory at a fresh temp dir, for the duration of the test.
// Returns the fake and the config directory.
func Install(t *testing.T) (*Fake, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))

	f := &Fake{Items: map[string]string{}}
	g, s, d := credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete
	credential.KeyringGet = func(_, key string) (string, error) {
		v, ok := f.Items[key]
		if !ok {
			// Must be the real sentinel: credential.get maps
			// keyring.ErrNotFound to ("", nil) and anything else to a hard
			// error, so a look-alike would turn every absent key into a
			// failure and quietly disable the paths under test.
			return "", keyring.ErrNotFound
		}
		return v, nil
	}
	credential.KeyringSet = func(_, key, value string) error {
		f.Sets = append(f.Sets, key)
		if f.FailSet != nil {
			if err := f.FailSet(key); err != nil {
				return err
			}
		}
		f.Items[key] = value
		return nil
	}
	credential.KeyringDelete = func(_, key string) error {
		f.Deletes = append(f.Deletes, key)
		if f.FailDelete != nil {
			if err := f.FailDelete(key); err != nil {
				return err
			}
		}
		if _, ok := f.Items[key]; !ok {
			return keyring.ErrNotFound
		}
		delete(f.Items, key)
		return nil
	}
	t.Cleanup(func() {
		credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete = g, s, d
	})
	return f, dir
}

// Has reports whether a key is stored.
func (f *Fake) Has(key string) bool { _, ok := f.Items[key]; return ok }

// Get returns the stored value, or "".
func (f *Fake) Get(key string) string { return f.Items[key] }

// ContextToken is the keyring key a named context's token is stored under.
// Spelled out here rather than exported from the production package so a change
// to the key layout has to be made in two places and noticed in review.
func ContextToken(name string) string { return "api_token::" + name }
