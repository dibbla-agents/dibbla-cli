package templates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifest_FindByID(t *testing.T) {
	m := &Manifest{
		Version: "1",
		Templates: []Template{
			{ID: "a", Name: "Alpha"},
			{ID: "b", Name: "Beta"},
		},
	}
	if got := m.FindByID("b"); got == nil || got.Name != "Beta" {
		t.Errorf("FindByID('b') failed; got %+v", got)
	}
	if got := m.FindByID("missing"); got != nil {
		t.Errorf("FindByID('missing') expected nil, got %+v", got)
	}
}

func TestManifest_JSONRoundTrip(t *testing.T) {
	orig := &Manifest{
		Version: "1",
		Templates: []Template{
			{ID: "x", Name: "X", Description: "desc", Category: "starter",
				BootstrapURL: "https://example.com/x.yaml",
				RepoURL:      "https://example.com/x.git",
				TemplatePath: "x-1",
				IconURL:      "https://example.com/x.svg",
				ReadmeURL:    "https://example.com/x.md",
			},
		},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round Manifest
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(round.Templates) != 1 || round.Templates[0].ID != "x" {
		t.Errorf("round-trip lost data: %+v", round)
	}
}

func TestManifestURL_EnvOverride(t *testing.T) {
	t.Setenv("DIBBLA_TEMPLATES_URL", "https://override.example/templates.json")
	if got := ManifestURL(); got != "https://override.example/templates.json" {
		t.Errorf("ManifestURL() = %q, want override", got)
	}
	t.Setenv("DIBBLA_TEMPLATES_URL", "")
	if got := ManifestURL(); got != DefaultManifestURL {
		t.Errorf("ManifestURL() default = %q, want %q", got, DefaultManifestURL)
	}
}

func TestCache_RoundTrip(t *testing.T) {
	// Point HOME at a temp dir so we don't touch the real cache file.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	manifestURL := "https://example.com/templates.json"
	orig := &Manifest{
		Version:   "1",
		Templates: []Template{{ID: "z", Name: "Zed"}},
	}
	if err := SaveCache(manifestURL, orig); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	// Cache file exists at the expected path.
	want := filepath.Join(tmp, ".dibbla", "templates-cache.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("cache file not at %s: %v", want, err)
	}

	entry, err := LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if entry.ManifestURL != manifestURL {
		t.Errorf("ManifestURL: got %q, want %q", entry.ManifestURL, manifestURL)
	}
	if len(entry.Manifest.Templates) != 1 || entry.Manifest.Templates[0].ID != "z" {
		t.Errorf("manifest not round-tripped: %+v", entry.Manifest)
	}
	if time.Since(entry.FetchedAt) > time.Minute {
		t.Errorf("FetchedAt seems wrong: %v", entry.FetchedAt)
	}
}

func TestLoadCache_Missing_ReturnsIsNotExist(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	_, err := LoadCache()
	if err == nil {
		t.Fatal("expected error when cache missing")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got %T: %v", err, err)
	}
}
