package templates

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	fetchTimeout  = 30 * time.Second
	maxManifestSz = 5 * 1024 * 1024
)

// Fetch downloads and parses the manifest at url. Returns an error if the
// response is non-2xx, the body exceeds 5 MB, the JSON is invalid, or the
// manifest version is not supported.
func Fetch(url string) (*Manifest, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSz+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if int64(len(body)) > maxManifestSz {
		return nil, fmt.Errorf("manifest at %s exceeds 5 MB limit", url)
	}

	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest at %s: %w", url, err)
	}
	if m.Version != SupportedVersion {
		return nil, fmt.Errorf("manifest version %q is not supported (this CLI supports v%s); upgrade dibbla", m.Version, SupportedVersion)
	}
	return &m, nil
}
