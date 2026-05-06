package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Method describes how the running dibbla binary was installed.
// `dibbla update` branches on this: it self-replaces only for Script
// installs, and prints the right command for everything else.
type Method int

const (
	MethodUnknown Method = iota
	MethodHomebrew
	MethodDebian
	MethodRPM
	MethodScoop
	MethodChocolatey
	MethodScript
	MethodGoInstall
	MethodSystemDir // /usr/bin or /usr/local/bin without a recognized package manager
)

func (m Method) String() string {
	switch m {
	case MethodHomebrew:
		return "homebrew"
	case MethodDebian:
		return "debian"
	case MethodRPM:
		return "rpm"
	case MethodScoop:
		return "scoop"
	case MethodChocolatey:
		return "chocolatey"
	case MethodScript:
		return "script"
	case MethodGoInstall:
		return "go-install"
	case MethodSystemDir:
		return "system-dir"
	default:
		return "unknown"
	}
}

// commandRunner lets tests stub out exec calls for dpkg/rpm lookups.
type commandRunner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) error
}

type realRunner struct{}

func (realRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (realRunner) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

var defaultRunner commandRunner = realRunner{}

// Detect classifies the install method of the currently running dibbla
// binary and returns the resolved (symlink-followed) path on disk.
//
// `version` is the build-time `cmd.Version` constant; "dev" short-circuits
// to MethodGoInstall regardless of where the binary lives.
func Detect(version string) (Method, string) {
	return detectWith(version, defaultRunner)
}

func detectWith(version string, r commandRunner) (Method, string) {
	if version == "dev" {
		path, _ := executablePath()
		return MethodGoInstall, path
	}

	path, err := executablePath()
	if err != nil {
		return MethodUnknown, ""
	}

	// On Windows, check Scoop / Chocolatey shim dirs first.
	if runtime.GOOS == "windows" {
		if isUnderScoop(path) {
			return MethodScoop, path
		}
		if isUnderChocolatey(path) {
			return MethodChocolatey, path
		}
		return MethodScript, path
	}

	// macOS Homebrew installs land under brew's prefix; the symlink in
	// /opt/homebrew/bin/dibbla resolves into Cellar/.
	if isUnderHomebrew(path) {
		return MethodHomebrew, path
	}

	// dpkg / rpm own the file iff querying it returns success.
	if _, err := r.LookPath("dpkg"); err == nil {
		if err := r.Run("dpkg", "-S", path); err == nil {
			return MethodDebian, path
		}
	}
	if _, err := r.LookPath("rpm"); err == nil {
		if err := r.Run("rpm", "-qf", path); err == nil {
			return MethodRPM, path
		}
	}

	// `go install` drops binaries in $GOPATH/bin or $HOME/go/bin. We can
	// only tell apart from a script install by Version: if it's "dev", we
	// already returned. With a real version someone has hand-built and
	// installed; treat as MethodGoInstall and refuse to self-replace.
	if isUnderGoBin(path) {
		return MethodGoInstall, path
	}

	// /usr/bin or /usr/local/bin without a package manager claiming it:
	// most likely a manual `cp`. Refuse to overwrite blindly.
	if strings.HasPrefix(path, "/usr/bin/") || strings.HasPrefix(path, "/usr/local/bin/") {
		return MethodSystemDir, path
	}

	return MethodScript, path
}

// executablePath returns the absolute, symlink-resolved path of the
// running binary.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return resolved, nil
}

// brewPrefixes is the set of path prefixes that indicate a Homebrew
// install. Apple-silicon brew lives under /opt/homebrew; Intel brew under
// /usr/local; some users set HOMEBREW_PREFIX explicitly.
func brewPrefixes() []string {
	prefixes := []string{
		"/opt/homebrew/Cellar/",
		"/opt/homebrew/bin/",
		"/usr/local/Cellar/",
	}
	if hp := os.Getenv("HOMEBREW_PREFIX"); hp != "" {
		prefixes = append(prefixes,
			filepath.Join(hp, "Cellar")+string(filepath.Separator),
			filepath.Join(hp, "bin")+string(filepath.Separator),
		)
	}
	return prefixes
}

func isUnderHomebrew(path string) bool {
	for _, p := range brewPrefixes() {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func isUnderScoop(path string) bool {
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(filepath.Join(home, "scoop")))
}

func isUnderChocolatey(path string) bool {
	choco := os.Getenv("ChocolateyInstall")
	if choco == "" {
		choco = `C:\ProgramData\chocolatey`
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(choco))
}

func isUnderGoBin(path string) bool {
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		if strings.HasPrefix(path, filepath.Clean(gobin)+string(filepath.Separator)) {
			return true
		}
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		bin := filepath.Join(gopath, "bin") + string(filepath.Separator)
		if strings.HasPrefix(path, bin) {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(path, filepath.Join(home, "go", "bin")+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// UpgradeCommand returns the recommended command for a given install
// method, or "" if the method is one we self-replace (or can't help with).
func UpgradeCommand(m Method) string {
	switch m {
	case MethodHomebrew:
		return "brew upgrade dibbla"
	case MethodDebian:
		return "sudo apt-get update && sudo apt-get install --only-upgrade dibbla"
	case MethodRPM:
		return "sudo dnf upgrade dibbla   # or: sudo yum upgrade dibbla"
	case MethodScoop:
		return "scoop update dibbla"
	case MethodChocolatey:
		return "choco upgrade dibbla"
	}
	return ""
}

// UninstallCommand returns the package-manager-native uninstall command
// for a given install method, or "" if dibbla manages removal itself
// (Script) or can't help (GoInstall, SystemDir, Unknown).
func UninstallCommand(m Method) string {
	switch m {
	case MethodHomebrew:
		return "brew uninstall dibbla"
	case MethodDebian:
		return "sudo apt-get remove dibbla"
	case MethodRPM:
		return "sudo dnf remove dibbla   # or: sudo yum remove dibbla"
	case MethodScoop:
		return "scoop uninstall dibbla"
	case MethodChocolatey:
		return "choco uninstall dibbla"
	}
	return ""
}
