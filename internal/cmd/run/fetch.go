package run

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	maxYAMLBytes  = 5 * 1024 * 1024
	fetchTimeout  = 30 * time.Second
)

// FetchYAML downloads a yaml task file from the given URL into a temp file.
// Returns the temp file path and a cleanup func that removes it.
func FetchYAML(url string) (path string, cleanup func(), err error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxYAMLBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	if int64(len(body)) > maxYAMLBytes {
		return "", nil, errors.New("task file exceeds 5 MB limit")
	}

	tmp, err := os.CreateTemp("", "dibbla-run-*.yaml")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("closing temp file: %w", err)
	}

	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}
