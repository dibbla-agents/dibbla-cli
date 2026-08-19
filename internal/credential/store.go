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

func get(key string) (string, error) {
	val, err := keyring.Get(serviceName, key)
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
	return keyring.Set(serviceName, keyToken, token)
}

// SetAPIURL stores the API URL in the OS credential store.
func SetAPIURL(url string) error {
	return keyring.Set(serviceName, keyAPIURL, url)
}

// DeleteToken removes the stored API token.
func DeleteToken() error {
	err := keyring.Delete(serviceName, keyToken)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// SetOrg stores the pinned organization id and display name.
func SetOrg(orgID, orgName string) error {
	if err := keyring.Set(serviceName, keyOrgID, orgID); err != nil {
		return err
	}
	return keyring.Set(serviceName, keyOrgName, orgName)
}

// DeleteOrg removes the pinned organization, returning the CLI to the org
// that the account itself defaults to.
func DeleteOrg() error {
	for _, k := range []string{keyOrgID, keyOrgName} {
		if err := keyring.Delete(serviceName, k); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return err
		}
	}
	return nil
}

// DeleteAPIURL removes the stored API URL.
func DeleteAPIURL() error {
	err := keyring.Delete(serviceName, keyAPIURL)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
