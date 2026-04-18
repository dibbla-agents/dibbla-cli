package templates

import (
	"fmt"
	"time"
)

// Source describes where a resolved manifest came from.
type Source int

const (
	// SourceFreshCache means the cache was used without a network attempt.
	SourceFreshCache Source = iota
	// SourceNetwork means the manifest was freshly fetched.
	SourceNetwork
)

// Resolution holds the resolved manifest plus metadata about where it came from.
type Resolution struct {
	Manifest *Manifest
	Source   Source
	Age      time.Duration // only meaningful when Source is SourceFreshCache
}

// Resolve returns the manifest, trying fresh-cache then network.
// If refresh is true, the fresh-cache short-circuit is skipped.
func Resolve(manifestURL string, refresh bool) (*Resolution, error) {
	// 1. Fresh cache (unless refresh forces network)
	if !refresh {
		if entry, err := LoadCache(); err == nil {
			age := time.Since(entry.FetchedAt)
			if age < FreshTTL {
				m := entry.Manifest
				return &Resolution{Manifest: &m, Source: SourceFreshCache, Age: age}, nil
			}
		}
	}

	// 2. Network
	m, err := Fetch(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("could not fetch template registry: %w", err)
	}
	_ = SaveCache(manifestURL, m)
	return &Resolution{Manifest: m, Source: SourceNetwork}, nil
}
