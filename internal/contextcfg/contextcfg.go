// Package contextcfg reads and writes the CLI's non-secret context list at
// ~/.config/dibbla/config.yaml. A context is a named login target: an API URL
// plus the organization pinned on that server. Tokens are NOT stored here;
// they live in the OS keyring (or the per-context credentials file fallback)
// under per-context keys. See internal/credential.
//
// The file is human-inspectable and hand-editable on purpose. Because it holds
// no secrets, being a plain enumerable YAML file is a feature: it is what makes
// `dibbla context list` reliable across macOS Keychain, Windows Credential
// Manager and libsecret, none of which offer a portable "enumerate everything
// stored under this service" API. It is strictly better than kubeconfig, which
// writes bearer tokens into the same file it enumerates.
//
// This package is deliberately dumb persistence with no notion of precedence,
// migration or "which context is active right now" — that lives one layer up in
// internal/config, which is also where DefaultAPIURL lives. The split exists to
// keep this package free of an import cycle back to config.
package contextcfg

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
)

const configFileName = "config.yaml"

// Context is a single named login target.
//
// APIURL is required. Org is the organization pinned on this server by
// `dibbla org use`, and it is behavioural rather than decorative: it becomes
// the X-Org-ID header on every authenticated request made through this
// context. It is per-context precisely because an org id is only meaningful on
// the server that issued it — sending one server's org id to another is a
// wrong-org read or write, which is the defect this whole design exists to
// avoid. OrgName is the display name recorded when the pin was made; it is
// never sent to any API and exists only so `dibbla status` and
// `dibbla context list` can print something a human recognizes without
// spending a round-trip resolving a UUID.
type Context struct {
	APIURL  string `yaml:"api_url"`
	Org     string `yaml:"org,omitempty"`
	OrgName string `yaml:"org_name,omitempty"`
}

// Config is the on-disk shape of ~/.config/dibbla/config.yaml.
type Config struct {
	Current  string             `yaml:"current,omitempty"`
	Contexts map[string]Context `yaml:"contexts,omitempty"`
}

// configFilePath resolves the config file path. Tests isolate it through
// cfgdir.SetForTest rather than through XDG_CONFIG_HOME, which os.UserConfigDir
// ignores on macOS.
func configFilePath() string {
	return cfgdir.Join(configFileName)
}

// Path returns the absolute path of the context config file. Empty string if
// the user config dir cannot be resolved.
func Path() string {
	return configFilePath()
}

// Exists reports whether the config file is present on disk. This is the
// migration guard: once the file exists, the legacy single-credential slots are
// never read as a source again.
func Exists() bool {
	path := configFilePath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// Load reads the config file. A missing file is not an error: it returns an
// empty but usable Config with a non-nil map, so callers can add contexts
// without a nil check.
//
// A malformed file IS an error, and deliberately so. The alternative — treating
// an unparseable config as "no contexts" — would silently drop a user who had
// carefully pointed the CLI at a customer instance back onto the production
// default, which is the one outcome a multi-server tool must not have.
func Load() (*Config, error) {
	cfg := &Config{Contexts: map[string]Context{}}
	path := configFilePath()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return cfg, nil
}

// Save writes the config file atomically at 0600, creating the parent
// directory at 0700 if needed.
func (c *Config) Save() error {
	path := configFilePath()
	if path == "" {
		return errors.New("could not resolve user config directory for context config")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal context config: %w", err)
	}
	return atomicWrite(path, data, 0600)
}

// Remove deletes the config file. No-op when it is already absent.
func Remove() error {
	path := configFilePath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Names returns the context names in sorted order, so every listing and every
// deterministic scan below reads the same way twice.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns the named context and whether it exists.
func (c *Config) Get(name string) (Context, bool) {
	ctx, ok := c.Contexts[name]
	return ctx, ok
}

// Set upserts a context under name.
func (c *Config) Set(name string, ctx Context) {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	c.Contexts[name] = ctx
}

// Delete removes a context. Removing one that does not exist is not an error.
func (c *Config) Delete(name string) {
	delete(c.Contexts, name)
	if c.Current == name {
		c.Current = ""
	}
}

// FindByURL returns the name of the context whose API URL matches apiURL after
// trailing-slash normalization, or "" if none does. This is what makes
// `dibbla login` against an already-known server refresh that context's token
// rather than accumulating a second context pointing at the same place.
func (c *Config) FindByURL(apiURL string) string {
	target := strings.TrimSuffix(strings.TrimSpace(apiURL), "/")
	if target == "" {
		return ""
	}
	// Deterministic across runs: scan names in sorted order rather than in Go's
	// randomized map order, so two identical configs never disagree.
	for _, name := range c.Names() {
		if strings.TrimSuffix(c.Contexts[name].APIURL, "/") == target {
			return name
		}
	}
	return ""
}

// DeriveName produces a context name from an API URL host: the default
// endpoint becomes "prod"; otherwise a leading "api." label is stripped and the
// first remaining DNS label is used, so api.haja.fatshark.se becomes "haja".
// Anything that does not survive that as a usable name becomes "server".
//
// The "api." strip applies only when there is another label behind it, so a
// bare internal host called "api" keeps its own name rather than being
// flattened to "server" — the strip exists to avoid every instance colliding
// on "api", and a host whose entire name is "api" is not that case.
//
// The result is a base name. Callers needing uniqueness pass it through
// UniqueName. defaultURL is the caller's notion of the default endpoint
// (config.DefaultAPIURL), passed in rather than imported to avoid a cycle.
//
// A slightly ugly derived name is never a trap, because names are renameable.
func DeriveName(apiURL, defaultURL string) string {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" || strings.TrimSuffix(apiURL, "/") == strings.TrimSuffix(defaultURL, "/") {
		return "prod"
	}
	host := apiURL
	if u, err := url.Parse(apiURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	host = strings.Trim(host, ".")
	labels := strings.Split(host, ".")
	// Drop a leading "api." so api.haja.fatshark.se derives "haja" and not the
	// useless "api", which every instance would collide on.
	if len(labels) > 1 && labels[0] == "api" {
		labels = labels[1:]
	}
	name := labels[0]
	// The name goes into a filename and a keyring key, so it has to satisfy the
	// same rule a hand-written one does. Deriving an unusable name from a
	// malformed URL and only discovering it at the write is worse than falling
	// back here.
	if !ValidName(name) {
		return "server"
	}
	return name
}

// UniqueName returns base if it is free in taken, otherwise the first of
// base-2, base-3, ... that is free. Deterministic, so re-deriving a name for
// the same input twice yields the same answer — which is what makes migration
// idempotent.
func UniqueName(base string, taken map[string]Context) string {
	if _, exists := taken[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

// ValidName reports whether name is usable as a context name. Names end up in
// keyring keys, in filenames (credentials.<name>.env) and in YAML keys, so the
// permissive set is deliberately narrow: letters, digits, dot, dash and
// underscore. Rejecting a path separator here is what stops a context named
// "../../evil" from writing a credentials file outside the config directory.
func ValidName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// atomicWrite writes data to path via a tempfile + rename in the same
// directory. Kept local rather than importing internal/env, which is
// .env-flavoured and would be the wrong dependency for a YAML file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tempfile: %w", err)
	}

	if runtime.GOOS != "windows" {
		// Set the mode before the rename so the file is never briefly
		// world-readable at its final path.
		if err := os.Chmod(tmpName, perm); err != nil {
			return fmt.Errorf("chmod tempfile: %w", err)
		}
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows refuses to rename onto an existing file.
		if runtime.GOOS == "windows" {
			if removeErr := os.Remove(path); removeErr == nil {
				if renameErr := os.Rename(tmpName, path); renameErr == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
