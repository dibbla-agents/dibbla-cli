package templates

import (
	"errors"
	"os"
	"time"
)

// Source describes where a resolved manifest came from.
type Source int

const (
	// SourceFreshCache means the cache was used without a network attempt.
	SourceFreshCache Source = iota
	// SourceNetwork means the manifest was freshly fetched.
	SourceNetwork
	// SourceStaleCache means the network fetch failed and we fell back to
	// a stale-but-usable cached copy.
	SourceStaleCache
	// SourceEmbedded means both network and cache were unusable, and we fell
	// back to the compiled-in minimal list.
	SourceEmbedded
)

// Resolution holds the resolved manifest plus metadata about where it came from.
type Resolution struct {
	Manifest  *Manifest
	Source    Source
	Age       time.Duration // only meaningful when Source is fresh/stale cache
	FetchErr  error         // only set when we had to fall back from network
}

// Resolve returns the manifest, trying fresh-cache → network → stale-cache → embedded.
// If refresh is true, the fresh-cache short-circuit is skipped.
func Resolve(manifestURL string, refresh bool) *Resolution {
	// 1. Fresh cache (unless refresh forces network)
	if !refresh {
		if entry, err := LoadCache(); err == nil {
			age := time.Since(entry.FetchedAt)
			if age < FreshTTL {
				m := entry.Manifest
				return &Resolution{Manifest: &m, Source: SourceFreshCache, Age: age}
			}
		}
	}

	// 2. Network
	m, err := Fetch(manifestURL)
	if err == nil {
		_ = SaveCache(manifestURL, m)
		return &Resolution{Manifest: m, Source: SourceNetwork}
	}

	// 3. Stale cache (within hard TTL)
	if entry, cacheErr := LoadCache(); cacheErr == nil {
		age := time.Since(entry.FetchedAt)
		if age < StaleTTL {
			m := entry.Manifest
			return &Resolution{Manifest: &m, Source: SourceStaleCache, Age: age, FetchErr: err}
		}
	} else if !errors.Is(cacheErr, os.ErrNotExist) {
		// ignore cache read errors other than "missing" — we just fall through
	}

	// 4. Embedded fallback
	return &Resolution{Manifest: Embedded(), Source: SourceEmbedded, FetchErr: err}
}
