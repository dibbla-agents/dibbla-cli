package credential

import (
	"errors"
	"strings"

	"github.com/99designs/keyring"
)

const (
	serviceName = "dibbla-cli"
	keyToken    = "api_token"
	keyAPIURL   = "api_url"
)

var errKeyNotFound = keyring.ErrKeyNotFound

func openKeyring() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName: serviceName,
	})
}

// GetCredentials returns both stored API token and API URL with a single keyring open,
// so the OS only prompts once (e.g. for keychain access) instead of once per item.
func GetCredentials() (token, apiURL string, err error) {
	ring, err := openKeyring()
	if err != nil {
		return "", "", err
	}
	token, _ = getItem(ring, keyToken)
	apiURL, _ = getItem(ring, keyAPIURL)
	return token, apiURL, nil
}

func getItem(ring keyring.Keyring, key string) (string, error) {
	item, err := ring.Get(key)
	if err != nil {
		if errors.Is(err, errKeyNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(item.Data)), nil
}

// GetToken returns the stored API token. Returns empty string and nil if not found
// so config.Load() can fall back cleanly.
func GetToken() (string, error) {
	ring, err := openKeyring()
	if err != nil {
		return "", err
	}
	return getItem(ring, keyToken)
}

// GetAPIURL returns the stored API URL. Returns empty string and nil if not found.
func GetAPIURL() (string, error) {
	ring, err := openKeyring()
	if err != nil {
		return "", err
	}
	return getItem(ring, keyAPIURL)
}

// SetToken stores the API token in the OS credential store.
func SetToken(token string) error {
	ring, err := openKeyring()
	if err != nil {
		return err
	}
	return ring.Set(keyring.Item{
		Key:  keyToken,
		Data: []byte(token),
	})
}

// SetAPIURL stores the API URL in the OS credential store.
func SetAPIURL(url string) error {
	ring, err := openKeyring()
	if err != nil {
		return err
	}
	return ring.Set(keyring.Item{
		Key:  keyAPIURL,
		Data: []byte(url),
	})
}

// DeleteToken removes the stored API token.
func DeleteToken() error {
	ring, err := openKeyring()
	if err != nil {
		return err
	}
	return ring.Remove(keyToken)
}

// DeleteAPIURL removes the stored API URL.
func DeleteAPIURL() error {
	ring, err := openKeyring()
	if err != nil {
		return err
	}
	return ring.Remove(keyAPIURL)
}
