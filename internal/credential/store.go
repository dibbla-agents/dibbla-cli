package credential

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/99designs/keyring"
)

const (
	serviceName = "dibbla-cli"
	keyToken    = "api_token"
	keyAPIURL   = "api_url"
)

var errKeyNotFound = keyring.ErrKeyNotFound

// fileKeyringDir returns a directory for the file keyring backend when no OS keychain is available.
func fileKeyringDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "~/.config/dibbla"
	}
	return filepath.Join(dir, "dibbla")
}

func openKeyring() (keyring.Keyring, error) {
	cfg := keyring.Config{
		ServiceName: serviceName,
		// When the file backend is used (e.g. Linux without Secret Service), these are required.
		FileDir:         fileKeyringDir(),
		FilePasswordFunc: keyring.TerminalPrompt,
	}
	return keyring.Open(cfg)
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
