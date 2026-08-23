package credential

import (
	"errors"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	serviceName = "dibbla-cli"
	keyToken    = "api_token"
	keyAPIURL   = "api_url"
	keyOrgID    = "org_id"
	keyOrgName  = "org_name"
)

// The three keyring operations are indirected through package-level vars so a
// test can make any one of them fail on demand.
//
// This is a deliverable rather than a convenience. Named contexts depend on an
// ordering guarantee — a token is written under the new key BEFORE the old key
// is deleted, in both migration and `dibbla context rename` — and the only way
// to test an ordering guarantee is to interrupt it. keyring.MockInit() cannot:
// it is process-global, offers no per-test isolation, and has no failure mode
// to inject. Without this seam the write-before-delete rule could be asserted
// only by reading the code, which is not a test.
//
// Rebindable from other packages' tests too, because internal/config is where
// migration lives and it must be able to fail a keyring write.
var (
	KeyringGet    = keyring.Get
	KeyringSet    = keyring.Set
	KeyringDelete = keyring.Delete
)

func get(key string) (string, error) {
	val, err := KeyringGet(serviceName, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	// Windows Credential Manager may return values with null bytes or
	// other invisible characters that TrimSpace does not remove.
	val = strings.TrimRight(val, "\x00")
	return strings.TrimSpace(val), nil
}

// GetCredentials returns both stored API token and API URL.
func GetCredentials() (token, apiURL string, err error) {
	token, err = get(keyToken)
	if err != nil {
		return "", "", err
	}
	apiURL, err = get(keyAPIURL)
	if err != nil {
		return "", "", err
	}
	return token, apiURL, nil
}

// GetToken returns the stored API token. Returns empty string and nil if not found
// so config.Load() can fall back cleanly.
func GetToken() (string, error) {
	return get(keyToken)
}

// GetAPIURL returns the stored API URL. Returns empty string and nil if not found.
func GetAPIURL() (string, error) {
	return get(keyAPIURL)
}

// GetOrg returns the pinned organization id and its display name, both empty
// when no org is pinned. The name is stored alongside the id purely so
// "dibbla status" can print something a human recognizes without spending a
// round-trip resolving the UUID.
func GetOrg() (orgID, orgName string, err error) {
	orgID, err = get(keyOrgID)
	if err != nil {
		return "", "", err
	}
	orgName, err = get(keyOrgName)
	if err != nil {
		return "", "", err
	}
	return orgID, orgName, nil
}

// SetToken stores the API token in the OS credential store.
func SetToken(token string) error {
	return KeyringSet(serviceName, keyToken, token)
}

// SetAPIURL stores the API URL in the OS credential store.
func SetAPIURL(url string) error {
	return KeyringSet(serviceName, keyAPIURL, url)
}

// DeleteToken removes the stored API token.
func DeleteToken() error {
	err := KeyringDelete(serviceName, keyToken)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// SetOrg stores the pinned organization id and display name.
func SetOrg(orgID, orgName string) error {
	if err := KeyringSet(serviceName, keyOrgID, orgID); err != nil {
		return err
	}
	return KeyringSet(serviceName, keyOrgName, orgName)
}

// DeleteOrg removes the pinned organization, returning the CLI to the org
// that the account itself defaults to.
func DeleteOrg() error {
	for _, k := range []string{keyOrgID, keyOrgName} {
		if err := KeyringDelete(serviceName, k); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
	}
	return nil
}

// DeleteAPIURL removes the stored API URL.
func DeleteAPIURL() error {
	err := KeyringDelete(serviceName, keyAPIURL)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// --- Per-context storage (P-0011) -------------------------------------------
//
// A context's token lives under its own keyring key rather than in the single
// legacy keyToken slot, which is what lets a user stay logged in to several API
// servers at once. The legacy slot is not retired: it is kept as a mirror of
// whichever context is current, so a binary that predates contexts — and every
// shipped script that reads the credentials file — keeps working unchanged.
// See internal/config/context.go for the mirroring rule.

// contextTokenKey is the per-context keyring key for a context's API token.
// The "::" separator cannot occur in a context name (contextcfg.ValidName
// permits only letters, digits, dot, dash and underscore), so the key space is
// unambiguous and a context can never collide with the legacy "api_token".
func contextTokenKey(name string) string {
	return keyToken + "::" + name
}

// SetContextToken stores a context's API token in the OS credential store.
func SetContextToken(name, token string) error {
	return KeyringSet(serviceName, contextTokenKey(name), token)
}

// GetContextToken returns the stored token for a context. Returns ("", nil)
// when there is none, so callers can fall back cleanly to the per-context
// credentials file rather than distinguishing "absent" from "broken".
func GetContextToken(name string) (string, error) {
	return get(contextTokenKey(name))
}

// DeleteContextToken removes a context's token. "Not found" is success.
func DeleteContextToken(name string) error {
	err := KeyringDelete(serviceName, contextTokenKey(name))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
