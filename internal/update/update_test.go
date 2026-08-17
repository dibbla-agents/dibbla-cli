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
		if r.URL.Path != "/latest.json" {
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

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

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

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

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

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	got := fetchLatest("1.0.0")
	if got != "" {
		t.Fatalf("expected empty string for invalid JSON, got %s", got)
	}
}

// useTempState rebinds stateFilePath for the duration of the test so
// readState/writeState hit a tempdir and don't read the developer's real
// ~/Library/Application Support/dibbla/state.yml on macOS, where
// XDG_CONFIG_HOME is ignored by os.UserConfigDir.
func useTempState(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	old := stateFilePath
	stateFilePath = func() string { return filepath.Join(tmp, "state.yml") }
	t.Cleanup(func() { stateFilePath = old })
	return tmp
}

func TestStateReadWrite(t *testing.T) {
	useTempState(t)

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
	tmp := useTempState(t)

	// Write a state that's 25 hours old
	old := &state{
		CheckedAt:     time.Now().UTC().Add(-25 * time.Hour),
		LatestVersion: "1.0.0",
	}
	data, _ := yaml.Marshal(old)
	os.WriteFile(filepath.Join(tmp, "state.yml"), data, 0644)

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
	tmp := useTempState(t)
	os.WriteFile(filepath.Join(tmp, "state.yml"), []byte("{{invalid yaml"), 0644)

	got := readState()
	if got != nil {
		t.Fatal("expected nil for corrupt state")
	}
}

func TestStateMissing(t *testing.T) {
	useTempState(t)

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
	// The notice points at Dibbla's own origin, not GitHub Releases: it is the
	// one URL that works everywhere the CLI runs, agent sandboxes included.
	if !strings.Contains(output, "https://install.dibbla.com") {
		t.Errorf("expected install URL in output, got: %s", output)
	}
	if strings.Contains(output, "github.com") {
		t.Errorf("notice should not send users to GitHub, got: %s", output)
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

func TestFetchRelease_LatestParsesAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest.json" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v3.4.5",
			"assets": []map[string]interface{}{
				{"name": "dibbla_3.4.5_linux_amd64.tar.gz", "browser_download_url": "https://example/a", "size": 100},
				{"name": "checksums.txt", "browser_download_url": "https://example/c", "size": 64},
			},
		})
	}))
	defer srv.Close()

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	rel, err := FetchRelease("1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v3.4.5" {
		t.Errorf("tag = %q want v3.4.5", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Fatalf("want 2 assets got %d", len(rel.Assets))
	}
	if rel.ChecksumAsset() == nil {
		t.Error("expected checksums.txt asset to be found")
	}
	if rel.FindAsset("dibbla_3.4.5_linux_amd64.tar.gz") == nil {
		t.Error("expected platform asset to be found")
	}
}

func TestFetchRelease_ByTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/dibbla-agents/dibbla-cli/releases/tags/v1.2.3" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"tag_name": "v1.2.3"})
	}))
	defer srv.Close()

	old := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = old }()

	rel, err := FetchRelease("1.0.0", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("tag = %q want v1.2.3", rel.TagName)
	}
}

func TestFetchRelease_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	if _, err := FetchRelease("1.0.0", ""); err == nil {
		t.Error("expected error on non-200")
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
	tmp := useTempState(t)

	s := &state{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: "1.5.0",
	}
	data, _ := yaml.Marshal(s)
	os.WriteFile(filepath.Join(tmp, "state.yml"), data, 0644)

	info := checkForUpdate("1.0.0")
	if info == nil {
		t.Fatal("expected non-nil info from cache")
	}
	if info.LatestVersion != "1.5.0" {
		t.Errorf("expected 1.5.0, got %s", info.LatestVersion)
	}
}

func TestCheckForUpdate_StaleCache(t *testing.T) {
	tmp := useTempState(t)

	s := &state{
		CheckedAt:     time.Now().UTC().Add(-25 * time.Hour),
		LatestVersion: "1.0.0",
	}
	data, _ := yaml.Marshal(s)
	os.WriteFile(filepath.Join(tmp, "state.yml"), data, 0644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	oldURL := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = oldURL }()

	info := checkForUpdate("1.0.0")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.LatestVersion != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", info.LatestVersion)
	}
}

func TestCheckForUpdate_FetchFailureRefreshesCheckedAt(t *testing.T) {
	tmp := useTempState(t)

	staleCheckedAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	s := &state{
		CheckedAt:     staleCheckedAt,
		LatestVersion: "v1.5.0",
	}
	data, _ := yaml.Marshal(s)
	os.WriteFile(filepath.Join(tmp, "state.yml"), data, 0644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	oldURL := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = oldURL }()

	info := checkForUpdate("1.0.0")
	if info != nil {
		t.Fatal("expected nil info when fetchLatest fails")
	}

	got := readState()
	if got == nil {
		t.Fatal("expected state to be written after failed fetch")
	}
	if !got.CheckedAt.After(staleCheckedAt) {
		t.Fatalf("expected checked_at to be refreshed; old=%v got=%v", staleCheckedAt, got.CheckedAt)
	}
	if time.Since(got.CheckedAt) > 5*time.Second {
		t.Fatalf("expected checked_at to be recent, got %v", got.CheckedAt)
	}
	if got.LatestVersion != "v1.5.0" {
		t.Fatalf("expected latest version to be preserved, got %q", got.LatestVersion)
	}
}

// --- P-0020: Dibbla's own mirror (latest.json) ---

func TestAssetSHA256(t *testing.T) {
	valid := strings.Repeat("ab", 32) // 64 hex chars
	cases := []struct {
		name   string
		digest string
		want   string
		ok     bool
	}{
		{"mirror asset", "sha256:" + valid, valid, true},
		{"uppercase hex is normalised", "sha256:" + strings.ToUpper(valid), valid, true},
		// A GitHub API asset simply has no digest; that is normal, and the
		// caller must fall back to checksums.txt rather than skip verification.
		{"absent", "", "", false},
		{"wrong algorithm", "sha512:" + valid, "", false},
		{"no prefix", valid, "", false},
		{"truncated", "sha256:" + valid[:63], "", false},
		{"not hex", "sha256:" + strings.Repeat("z", 64), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Asset{Digest: tc.digest}
			got, ok := a.SHA256()
			if ok != tc.ok {
				t.Fatalf("ok = %v want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("digest = %q want %q", got, tc.want)
			}
		})
	}
}

func TestFetchRelease_MirrorLatestJSONCarriesDigests(t *testing.T) {
	archiveDigest := strings.Repeat("1", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest.json" {
			t.Errorf("unpinned update must read /latest.json, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v1.2.54",
			"assets": []map[string]interface{}{
				{
					"name":                 "dibbla_1.2.54_linux_amd64.tar.gz",
					"browser_download_url": "https://install.dibbla.com/latest/dibbla_1.2.54_linux_amd64.tar.gz",
					"size":                 4933635,
					"digest":               "sha256:" + archiveDigest,
				},
				{
					"name":                 "checksums.txt",
					"browser_download_url": "https://install.dibbla.com/checksums.txt",
					"size":                 1065,
				},
			},
		})
	}))
	defer srv.Close()

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	rel, err := FetchRelease("1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.54" {
		t.Errorf("tag = %q want v1.2.54", rel.TagName)
	}

	// The whole point of the shape: FindAsset/ChecksumAsset are indifferent to
	// which origin answered, so the self-replace flow needs no branch.
	asset := rel.FindAsset("dibbla_1.2.54_linux_amd64.tar.gz")
	if asset == nil {
		t.Fatal("expected the platform archive to be found")
	}
	if asset.DownloadURL != "https://install.dibbla.com/latest/dibbla_1.2.54_linux_amd64.tar.gz" {
		t.Errorf("download URL must stay on our own origin, got %q", asset.DownloadURL)
	}
	got, ok := asset.SHA256()
	if !ok || got != archiveDigest {
		t.Errorf("digest = %q ok=%v want %q true", got, ok, archiveDigest)
	}
	if rel.ChecksumAsset() == nil {
		t.Error("expected checksums.txt asset for the no-digest fallback")
	}
}

func TestFetchRelease_MalformedLatestJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Truncated mid-object: what a half-written mirror deploy looks like.
		w.Write([]byte(`{"tag_name": "v1.2.54", "assets": [{"name":`))
	}))
	defer srv.Close()

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	if _, err := FetchRelease("1.0.0", ""); err == nil {
		t.Error("expected an error on malformed latest.json, not a zero-value Release")
	}
}

// A pinned --version has no mirror equivalent, and GitHub is exactly what is
// unreachable in the environments P-0020 exists for. The error has to say so:
// a bare i/o timeout reads as a broken network and sends people hunting in the
// wrong place.
func TestFetchRelease_PinnedTagErrorNamesTheWayOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // what an agent sandbox actually returns
	}))
	defer srv.Close()

	old := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = old }()

	_, err := FetchRelease("1.0.0", "v1.2.52")
	if err == nil {
		t.Fatal("expected an error when GitHub is unreachable for a pinned tag")
	}
	for _, want := range []string{"--version", "install.dibbla.com", "without --version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q; got: %v", want, err)
		}
	}
}

// The mirror path must not be wrapped with the pinned-tag advice — suggesting
// "run without --version" to someone who already did would be nonsense.
func TestFetchRelease_UnpinnedErrorIsNotWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	old := mirrorBaseURL
	mirrorBaseURL = srv.URL
	defer func() { mirrorBaseURL = old }()

	_, err := FetchRelease("1.0.0", "")
	if err == nil {
		t.Fatal("expected an error on a 503 from the mirror")
	}
	if strings.Contains(err.Error(), "--version") {
		t.Errorf("unpinned failure must not carry the pinned-tag advice; got: %v", err)
	}
}
