package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/mattn/go-isatty"
	"gopkg.in/yaml.v3"
)

// apiBaseURL is the GitHub API base URL. Override in tests.
var apiBaseURL = "https://api.github.com"

const checkInterval = 24 * time.Hour

// UpdateInfo holds the result of an update check.
type UpdateInfo struct {
	LatestVersion string
}

// Asset describes one file attached to a GitHub release.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

// Release is the subset of the GitHub release payload we care about.
// Shared between the background notifier and the `dibbla update` command
// so both go through one HTTP path.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// ChecksumAsset returns the asset named "checksums.txt" if present.
func (r *Release) ChecksumAsset() *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == "checksums.txt" {
			return &r.Assets[i]
		}
	}
	return nil
}

// FindAsset returns the first asset whose name matches the given name.
func (r *Release) FindAsset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// state represents the cached update check state on disk.
type state struct {
	CheckedAt     time.Time `yaml:"checked_at"`
	LatestVersion string    `yaml:"latest_version"`
}

// StateFilePath exposes the cached-state path so other packages
// (notably the update command) can refresh it after a successful upgrade.
func StateFilePath() string { return stateFilePath() }

// WriteCachedLatest stores the latest known version with a fresh timestamp.
// Used by `dibbla update` to suppress the background notice immediately
// after a successful self-replace.
func WriteCachedLatest(version string) {
	writeState(&state{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: version,
	})
}

// CheckInBackground starts an update check in a goroutine and returns a channel
// that receives the result. Returns nil if the check should be skipped.
func CheckInBackground(currentVersion string) <-chan *UpdateInfo {
	if currentVersion == "dev" {
		return nil
	}
	if platform.IsCI() {
		return nil
	}
	if os.Getenv("DIBBLA_NO_UPDATE_NOTIFIER") != "" {
		return nil
	}
	if !isatty.IsTerminal(os.Stderr.Fd()) && !isatty.IsCygwinTerminal(os.Stderr.Fd()) {
		return nil
	}

	ch := make(chan *UpdateInfo, 1)
	go func() {
		defer close(ch)
		info := checkForUpdate(currentVersion)
		ch <- info
	}()
	return ch
}

// PrintNotice prints a styled update notice to stderr if a newer version is available.
func PrintNotice(info *UpdateInfo, currentVersion string) {
	if info == nil {
		return
	}

	current, err := semver.NewVersion(strings.TrimPrefix(currentVersion, "v"))
	if err != nil {
		return
	}
	latest, err := semver.NewVersion(strings.TrimPrefix(info.LatestVersion, "v"))
	if err != nil {
		return
	}

	if !latest.GreaterThan(current) {
		return
	}

	icon := platform.Icon("✦", "*")
	fmt.Fprintf(os.Stderr, "\n%s A new version of dibbla is available: v%s → v%s\n", icon, current.String(), latest.String())
	fmt.Fprintln(os.Stderr, "  https://github.com/dibbla-agents/dibbla-cli/releases/latest")
}

func checkForUpdate(currentVersion string) *UpdateInfo {
	s := readState()
	if s != nil && time.Since(s.CheckedAt) < checkInterval {
		if s.LatestVersion == "" {
			return nil
		}
		return &UpdateInfo{LatestVersion: s.LatestVersion}
	}

	latest := fetchLatest(currentVersion)
	if latest == "" {
		cachedLatest := ""
		if s != nil {
			cachedLatest = s.LatestVersion
		}
		writeState(&state{
			CheckedAt:     time.Now().UTC(),
			LatestVersion: cachedLatest,
		})
		return nil
	}

	writeState(&state{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: latest,
	})

	return &UpdateInfo{LatestVersion: latest}
}

func stateFilePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "dibbla", "state.yml")
}

func readState() *state {
	path := stateFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s state
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func writeState(s *state) {
	path := stateFilePath()
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func fetchLatest(currentVersion string) string {
	rel, err := FetchRelease(currentVersion, "")
	if err != nil || rel == nil {
		return ""
	}
	return rel.TagName
}

// FetchRelease retrieves a release from the dibbla-cli GitHub repo. If tag
// is empty the "latest" release is returned; otherwise the release with the
// given tag (e.g. "v1.2.3") is fetched. The notifier and the `dibbla update`
// command both go through this function.
func FetchRelease(currentVersion, tag string) (*Release, error) {
	var url string
	if tag == "" {
		url = apiBaseURL + "/repos/dibbla-agents/dibbla-cli/releases/latest"
	} else {
		url = apiBaseURL + "/repos/dibbla-agents/dibbla-cli/releases/tags/" + tag
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dibbla-cli/"+currentVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release Release
	// Limit response to 1MB to prevent memory exhaustion from unexpected responses.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}
