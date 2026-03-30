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

// state represents the cached update check state on disk.
type state struct {
	CheckedAt     time.Time `yaml:"checked_at"`
	LatestVersion string    `yaml:"latest_version"`
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
		return &UpdateInfo{LatestVersion: s.LatestVersion}
	}

	latest := fetchLatest(currentVersion)
	if latest == "" {
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
	url := apiBaseURL + "/repos/dibbla-agents/dibbla-cli/releases/latest"

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dibbla-cli/"+currentVersion)

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	// Limit response to 1MB to prevent memory exhaustion from unexpected responses.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return ""
	}

	return release.TagName
}
