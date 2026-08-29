package credential

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"

	"github.com/dibbla-agents/dibbla-cli/internal/env"
)

// --- Platform-connector OAuth grants (P-0035 E5, DIB-544) --------------------
//
// A grant is what `dibbla mcp platform --login` obtains: an access token, the
// refresh token that renews it, and the issuer/resource/client it was minted
// for. It is a bearer credential exactly like the API token, so it lives where
// the API token lives — the OS keyring, or on keyring-less hosts the context's
// own credentials file — and never in config.yaml or any client config file.
//
// The grant is keyed per context because a grant is only meaningful against
// the server that issued it: a token from auth.<one-domain> presented to
// mcp.<another-domain> is refused, and storing it per context is what keeps
// `dibbla context use` from silently changing which grant a check exercises.
//
// The payload is opaque to this package: JSON serialised by internal/platformoauth.

const (
	keyPlatformGrant     = "platform_grant"
	filePlatformGrantKey = "DIBBLA_PLATFORM_GRANT"
)

func platformGrantKey(context string) string {
	return keyPlatformGrant + "::" + context
}

// SetPlatformGrant stores a context's platform grant in the OS keyring.
func SetPlatformGrant(context string, payload []byte) error {
	return KeyringSet(serviceName, platformGrantKey(context), string(payload))
}

// GetPlatformGrant returns a context's platform grant from the OS keyring.
// (nil, nil) when there is none, so the caller can fall back to the file.
func GetPlatformGrant(context string) ([]byte, error) {
	v, err := get(platformGrantKey(context))
	if err != nil || v == "" {
		return nil, err
	}
	return []byte(v), nil
}

// DeletePlatformGrant removes a context's platform grant from the OS keyring.
// "Not found" is success.
func DeletePlatformGrant(context string) error {
	err := KeyringDelete(serviceName, platformGrantKey(context))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// SetPlatformGrantFile writes a context's platform grant into the context's
// credentials file (credentials.<name>.env), base64url-encoded so the JSON
// survives dotenv quoting rules untouched. The token and API URL entries in
// the same file are left alone.
func SetPlatformGrantFile(context string, payload []byte) error {
	path := contextFilePath(context)
	if path == "" {
		return fmt.Errorf("cannot resolve a credentials file for context %q", context)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	if _, err := env.MergeEnvFile(path, map[string]string{filePlatformGrantKey: enc}); err != nil {
		return err
	}
	return nil
}

// GetPlatformGrantFile reads a context's platform grant from its credentials
// file. (nil, nil) when the file or the entry does not exist.
func GetPlatformGrantFile(context string) ([]byte, error) {
	vars, err := readCredFileAt(contextFilePath(context))
	if err != nil {
		return nil, err
	}
	enc := vars[filePlatformGrantKey]
	if enc == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("the stored platform grant in %s is not readable: %w", contextFilePath(context), err)
	}
	return raw, nil
}

// DeletePlatformGrantFile clears the grant entry from a context's credentials
// file. The key is blanked rather than removed — MergeEnvFile has no delete
// mode — and an empty value reads back as "no grant". No-op when the file does
// not exist.
func DeletePlatformGrantFile(context string) error {
	path := contextFilePath(context)
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, err := env.MergeEnvFile(path, map[string]string{filePlatformGrantKey: ""})
	return err
}
