package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestCheckInBackground_SkipsDev(t *testing.T) {
	ch := CheckInBackground("dev")
	if ch != nil {
		t.Fatal("expected nil channel for dev version")
	}
}

func TestCheckInBackground_SkipsEnvVar(t *testing.T) {
	t.Setenv("DIBBLA_NO_UPDATE_NOTIFIER", "1")
	ch := CheckInBackground("1.0.0")
	if ch != nil {
		t.Fatal("expected nil channel when DIBBLA_NO_UPDATE_NOTIFIER is set")
	}
}

func TestCheckInBackground_SkipsCI(t *testing.T) {
	t.Setenv("CI", "true")
	ch := CheckInBackground("1.0.0")
	if ch != nil {
		t.Fatal("expected nil channel in CI")
	}
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/dibbla-agents/dibbla-cli/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Error("missing Accept header")
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	got := fetchLatest("1.0.0")
	if got != "v2.0.0" {
		t.Fatalf("expected v2.0.0, got %s", got)
	}
}

func TestFetchLatest_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	got := fetchLatest("1.0.0")
	if got != "" {
		t.Fatalf("expected empty string for non-200, got %s", got)
	}
}

func TestFetchLatest_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	got := fetchLatest("1.0.0")
	if got != "" {
		t.Fatalf("expected empty string for invalid JSON, got %s", got)
	}
}

func TestStateReadWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp) // Linux
	// Also handle macOS/Windows by overriding the function indirectly
	// os.UserConfigDir() respects XDG_CONFIG_HOME on Linux

	dir := filepath.Join(tmp, "dibbla")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	s := &state{
		CheckedAt:     now,
		LatestVersion: "1.2.3",
	}
	writeState(s)

	got := readState()
	if got == nil {
		t.Fatal("expected non-nil state")
	}
	if got.LatestVersion != "1.2.3" {
		t.Errorf("expected 1.2.3, got %s", got.LatestVersion)
	}
	if !got.CheckedAt.Equal(now) {
		t.Errorf("expected %v, got %v", now, got.CheckedAt)
	}
}

func TestStateExpiry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "dibbla")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a state that's 25 hours old
	old := &state{
		CheckedAt:     time.Now().UTC().Add(-25 * time.Hour),
		LatestVersion: "1.0.0",
	}
	data, _ := yaml.Marshal(old)
	os.WriteFile(filepath.Join(dir, "state.yml"), data, 0644)

	got := readState()
	if got == nil {
		t.Fatal("expected non-nil state")
	}

	// The state should be readable, but checkForUpdate should treat it as stale
	if time.Since(got.CheckedAt) < checkInterval {
		t.Error("expected state to be stale")
	}
}

func TestStateCorrupt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "dibbla")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "state.yml"), []byte("{{invalid yaml"), 0644)

	got := readState()
	if got != nil {
		t.Fatal("expected nil for corrupt state")
	}
}

func TestStateMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got := readState()
	if got != nil {
		t.Fatal("expected nil for missing state")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestPrintNotice_NewerVersion(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "v2.0.0"}, "1.0.0")
	})
	if output == "" {
		t.Fatal("expected notice output")
	}
	if !strings.Contains(output, "1.0.0") || !strings.Contains(output, "2.0.0") {
		t.Errorf("expected version numbers in output, got: %s", output)
	}
	if !strings.Contains(output, "https://github.com/dibbla-agents/dibbla-cli/releases/latest") {
		t.Errorf("expected release URL in output, got: %s", output)
	}
}

func TestPrintNotice_SameVersion(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "v1.0.0"}, "1.0.0")
	})
	if output != "" {
		t.Fatalf("expected no output for same version, got: %s", output)
	}
}

func TestPrintNotice_OlderVersion(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "v0.5.0"}, "1.0.0")
	})
	if output != "" {
		t.Fatalf("expected no output for older version, got: %s", output)
	}
}

func TestPrintNotice_Nil(t *testing.T) {
	// Should not panic
	PrintNotice(nil, "1.0.0")
}

func TestPrintNotice_InvalidVersion(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "not-semver"}, "1.0.0")
	})
	if output != "" {
		t.Fatalf("expected no output for invalid version, got: %s", output)
	}
}

func TestPrintNotice_InvalidCurrentVersion(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "v2.0.0"}, "not-semver")
	})
	if output != "" {
		t.Fatalf("expected no output for invalid current version, got: %s", output)
	}
}

func TestPrintNotice_VPrefixHandling(t *testing.T) {
	output := captureStderr(t, func() {
		PrintNotice(&UpdateInfo{LatestVersion: "v2.0.0"}, "v1.0.0")
	})
	if output == "" {
		t.Fatal("expected notice output with v-prefix")
	}
}

func TestCheckForUpdate_CachedFresh(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "dibbla")
	os.MkdirAll(dir, 0755)

	s := &state{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: "1.5.0",
	}
	data, _ := yaml.Marshal(s)
	os.WriteFile(filepath.Join(dir, "state.yml"), data, 0644)

	info := checkForUpdate("1.0.0")
	if info == nil {
		t.Fatal("expected non-nil info from cache")
	}
	if info.LatestVersion != "1.5.0" {
		t.Errorf("expected 1.5.0, got %s", info.LatestVersion)
	}
}

func TestCheckForUpdate_StaleCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "dibbla")
	os.MkdirAll(dir, 0755)

	s := &state{
		CheckedAt:     time.Now().UTC().Add(-25 * time.Hour),
		LatestVersion: "1.0.0",
	}
	data, _ := yaml.Marshal(s)
	os.WriteFile(filepath.Join(dir, "state.yml"), data, 0644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	oldURL := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = oldURL }()

	info := checkForUpdate("1.0.0")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.LatestVersion != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", info.LatestVersion)
	}
}

