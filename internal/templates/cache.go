package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// FreshTTL is how long a cached manifest is considered fresh enough to
	// use without attempting a network refresh.
	FreshTTL = 1 * time.Hour
	// StaleTTL is the hard cutoff beyond which we prefer an embedded fallback
	// over a very stale cache when the network is unavailable.
	StaleTTL = 24 * time.Hour
)

// CacheEntry is the envelope stored on disk at ~/.dibbla/templates-cache.json.
type CacheEntry struct {
	FetchedAt   time.Time `json:"fetched_at"`
	ManifestURL string    `json:"manifest_url"`
	Manifest    Manifest  `json:"manifest"`
}

// cachePath returns the absolute path to the cache file.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".dibbla", "templates-cache.json"), nil
}

// LoadCache reads the cache file. Returns os.ErrNotExist if it doesn't exist.
func LoadCache() (*CacheEntry, error) {
	p, err := cachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var e CacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parsing cache at %s: %w", p, err)
	}
	return &e, nil
}

// SaveCache writes the manifest to disk.
func SaveCache(manifestURL string, m *Manifest) error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	e := CacheEntry{
		FetchedAt:   time.Now().UTC(),
		ManifestURL: manifestURL,
		Manifest:    *m,
	}
	data, err := json.MarshalIndent(&e, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	return nil
}
