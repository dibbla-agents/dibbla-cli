package skillregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "skill-installs.json")
	cleanup := SetPathForTest(path)
	t.Cleanup(cleanup)
	return path
}

func TestLoad_MissingFile_ReturnsEmpty(t *testing.T) {
	setup(t)
	r := Load()
	if r == nil {
		t.Fatal("Load returned nil")
	}
	if len(r.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(r.Entries))
	}
	if r.Version != currentVersion {
		t.Errorf("expected version %d, got %d", currentVersion, r.Version)
	}
}

func TestLoad_MalformedFile_ReturnsEmpty(t *testing.T) {
	path := setup(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Load()
	if len(r.Entries) != 0 {
		t.Errorf("expected empty entries on malformed file, got %d", len(r.Entries))
	}
}

func TestRecord_AppendsAndDeduplicates(t *testing.T) {
	setup(t)
	if err := Record("dibbla", "/a"); err != nil {
		t.Fatal(err)
	}
	if err := Record("dibbla", "/b"); err != nil {
		t.Fatal(err)
	}
	if err := Record("dibbla", "/a"); err != nil { // dup
		t.Fatal(err)
	}
	got := Entries()
	if len(got) != 2 {
		t.Errorf("expected 2 entries (dedup), got %d: %+v", len(got), got)
	}
}

func TestForget_RemovesEntry(t *testing.T) {
	setup(t)
	_ = Record("dibbla", "/a")
	_ = Record("dibbla", "/b")
	if err := Forget("dibbla", "/a"); err != nil {
		t.Fatal(err)
	}
	got := Entries()
	if len(got) != 1 || got[0].Root != "/b" {
		t.Errorf("expected only /b to remain, got %+v", got)
	}
}

func TestForget_MissingEntryIsNoOp(t *testing.T) {
	setup(t)
	_ = Record("dibbla", "/a")
	if err := Forget("dibbla", "/never-recorded"); err != nil {
		t.Fatal(err)
	}
	got := Entries()
	if len(got) != 1 {
		t.Errorf("expected /a to remain, got %+v", got)
	}
}

func TestClear_RemovesFile(t *testing.T) {
	path := setup(t)
	_ = Record("dibbla", "/a")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("registry file should exist: %v", err)
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file gone, got err=%v", err)
	}
}

func TestClear_MissingFileIsNoOp(t *testing.T) {
	setup(t)
	if err := Clear(); err != nil {
		t.Errorf("Clear on missing file should be no-op, got %v", err)
	}
}

func TestEntries_StableSort(t *testing.T) {
	setup(t)
	_ = Record("dibbla", "/c")
	_ = Record("dibbla", "/a")
	_ = Record("dibbla", "/b")
	got := Entries()
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].Root != "/a" || got[1].Root != "/b" || got[2].Root != "/c" {
		t.Errorf("expected /a /b /c, got %s %s %s", got[0].Root, got[1].Root, got[2].Root)
	}
}

func TestRecord_RoundTripsViaDisk(t *testing.T) {
	path := setup(t)
	if err := Record("dibbla", "/x"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("on-disk file is not valid JSON: %v", err)
	}
	if r.Version != currentVersion {
		t.Errorf("version not persisted: got %d, want %d", r.Version, currentVersion)
	}
	if len(r.Entries) != 1 || r.Entries[0].Root != "/x" {
		t.Errorf("unexpected on-disk entries: %+v", r.Entries)
	}
}
