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

// mirrorBaseURL is the origin serving Dibbla's own release mirror
// (latest.json + /latest/<archive> + checksums.txt). Override in tests.
//
// The unpinned update path resolves here rather than at api.github.com because
// both api.github.com and github.com return 403 inside Anthropic-hosted agent
// sandboxes, where the block is repository-scoped by Anthropic and cannot be
// allowlisted away. See P-0020.
var mirrorBaseURL = defaultMirrorBaseURL()

// defaultMirrorBaseURL honours DIBBLA_INSTALL_BASE_URL, the same variable both
// bootstrap installers read.
//
// Without it, `dibbla update` could not be pointed at a staging deploy at all,
// so the mitigation the mirror relies on — verify at the install-*.dibbla.app
// alias before DNS moves onto it — covered only the two shell scripts and left
// half the client surface unverifiable before a cutover.
func defaultMirrorBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("DIBBLA_INSTALL_BASE_URL")), "/"); v != "" {
		return v
	}
	return "https://install.dibbla.com"
}

// githubAPIBaseURL still serves `dibbla update --version <tag>`: the mirror is
// latest-only (it would not fit the platform's 50 MB upload limit otherwise), so
// a pinned tag has no mirror equivalent. Override in tests.
var githubAPIBaseURL = "https://api.github.com"

const checkInterval = 24 * time.Hour

// UpdateInfo holds the result of an update check.
type UpdateInfo struct {
	LatestVersion string
}

// Asset describes one file attached to a release.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`

	// Digest is "sha256:" + 64 lowercase hex, following the convention
	// P-0016 established for the hosted agent-skill index. Present on
	// assets from Dibbla's own mirror, absent on the GitHub API payload
	// (so an empty value is normal, not an error) — which is why
	// checksums.txt remains the fallback rather than being replaced.
	Digest string `json:"digest"`
}

// SHA256 returns the bare hex digest from Digest, and false when the field is
// absent or not in the expected "sha256:<64 hex>" form. Anything unparseable is
// treated as absent so the caller falls back to checksums.txt rather than
// skipping verification.
func (a *Asset) SHA256() (string, bool) {
	const prefix = "sha256:"
	if !strings.HasPrefix(a.Digest, prefix) {
		return "", false
	}
	hex := strings.ToLower(strings.TrimPrefix(a.Digest, prefix))
	if len(hex) != 64 {
		return "", false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return hex, true
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
	fmt.Fprintln(os.Stderr, "  https://install.dibbla.com")
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

// stateFilePath is overridable in tests (mirrors the apiBaseURL pattern
// above). On macOS, os.UserConfigDir ignores XDG_CONFIG_HOME and always
// returns ~/Library/Application Support, so tests can't isolate state by
// env vars alone — they rebind this variable instead.
var stateFilePath = func() string {
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

// FetchRelease retrieves a release descriptor. If tag is empty it reads
// latest.json from Dibbla's own mirror; otherwise it resolves the pinned tag
// against the GitHub API, which is the only place historical releases exist.
// The notifier and the `dibbla update` command both go through this function.
//
// The two paths return the same shape on purpose: latest.json carries exactly
// the subset decoded here, so FindAsset/ChecksumAsset and the whole self-replace
// flow are indifferent to which origin answered.
func FetchRelease(currentVersion, tag string) (*Release, error) {
	pinned := tag != ""

	var url string
	if pinned {
		url = githubAPIBaseURL + "/repos/dibbla-agents/dibbla-cli/releases/tags/" + tag
	} else {
		url = mirrorBaseURL + "/latest.json"
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
		return nil, wrapPinnedTagError(pinned, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if pinned {
			return nil, wrapPinnedTagError(pinned, fmt.Errorf("GitHub API returned status %d", resp.StatusCode))
		}
		return nil, fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}

	var release Release
	// Limit response to 1MB to prevent memory exhaustion from unexpected responses.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, wrapPinnedTagError(pinned, err)
	}

	return &release, nil
}

// wrapPinnedTagError explains the one case where `dibbla update` still needs
// GitHub. The mirror is latest-only, so a pinned `--version` has nowhere else to
// resolve — and in the environments P-0020 exists for (agent sandboxes, locked
// down CI) GitHub is exactly what is unreachable. Naming the unpinned command as
// the way out matters: a bare "dial tcp: i/o timeout" reads as a broken network
// and sends people hunting in the wrong place.
func wrapPinnedTagError(pinned bool, err error) error {
	if !pinned || err == nil {
		return err
	}
	return fmt.Errorf("%w\n\n"+
		"Pinned updates (--version) resolve against GitHub, which some environments\n"+
		"block — agent sandboxes and restricted CI among them. Dibbla's own mirror at\n"+
		"%s serves the latest release only.\n"+
		"Run `dibbla update` without --version to upgrade via the mirror.", err, mirrorBaseURL)
}
