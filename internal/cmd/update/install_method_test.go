package update

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

type stubRunner struct {
	hasDpkg bool
	hasRpm  bool
	dpkgErr error
	rpmErr  error
}

func (s stubRunner) LookPath(name string) (string, error) {
	switch name {
	case "dpkg":
		if s.hasDpkg {
			return "/usr/bin/dpkg", nil
		}
	case "rpm":
		if s.hasRpm {
			return "/usr/bin/rpm", nil
		}
	}
	return "", errors.New("not found")
}

func (s stubRunner) Run(name string, args ...string) error {
	switch name {
	case "dpkg":
		return s.dpkgErr
	case "rpm":
		return s.rpmErr
	}
	return errors.New("unexpected command")
}

func TestUpgradeCommand(t *testing.T) {
	cases := []struct {
		m        Method
		contains string
	}{
		{MethodHomebrew, "brew upgrade dibbla"},
		{MethodDebian, "apt-get install --only-upgrade dibbla"},
		{MethodRPM, "dnf upgrade dibbla"},
		{MethodScoop, "scoop update dibbla"},
		{MethodChocolatey, "choco upgrade dibbla"},
	}
	for _, c := range cases {
		got := UpgradeCommand(c.m)
		if !strings.Contains(got, c.contains) {
			t.Errorf("UpgradeCommand(%v) = %q, want substring %q", c.m, got, c.contains)
		}
	}
	if got := UpgradeCommand(MethodScript); got != "" {
		t.Errorf("expected empty for MethodScript, got %q", got)
	}
}

func TestUninstallCommand(t *testing.T) {
	cases := []struct {
		m        Method
		contains string
	}{
		{MethodHomebrew, "brew uninstall dibbla"},
		{MethodDebian, "apt-get remove dibbla"},
		{MethodRPM, "dnf remove dibbla"},
		{MethodScoop, "scoop uninstall dibbla"},
		{MethodChocolatey, "choco uninstall dibbla"},
	}
	for _, c := range cases {
		got := UninstallCommand(c.m)
		if !strings.Contains(got, c.contains) {
			t.Errorf("UninstallCommand(%v) = %q, want substring %q", c.m, got, c.contains)
		}
	}
	for _, m := range []Method{MethodScript, MethodGoInstall, MethodSystemDir, MethodUnknown} {
		if got := UninstallCommand(m); got != "" {
			t.Errorf("expected empty for %v, got %q", m, got)
		}
	}
}

func TestMethodString(t *testing.T) {
	for _, m := range []Method{
		MethodUnknown, MethodHomebrew, MethodDebian, MethodRPM,
		MethodScoop, MethodChocolatey, MethodScript, MethodGoInstall, MethodSystemDir,
	} {
		if m.String() == "" {
			t.Errorf("Method(%d).String() returned empty", m)
		}
	}
}

func TestIsUnderHomebrew(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/Cellar/dibbla/0.1.0/bin/dibbla", true},
		{"/usr/local/Cellar/dibbla/0.1.0/bin/dibbla", true},
		{"/opt/homebrew/bin/dibbla", true},
		{"/usr/local/bin/dibbla", false},
		{"/home/user/.local/bin/dibbla", false},
	}
	for _, c := range cases {
		if got := isUnderHomebrew(c.path); got != c.want {
			t.Errorf("isUnderHomebrew(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsUnderGoBin(t *testing.T) {
	t.Setenv("GOBIN", "/tmp/gobin")
	if !isUnderGoBin("/tmp/gobin/dibbla") {
		t.Error("expected /tmp/gobin/dibbla to be under GOBIN")
	}
	if isUnderGoBin("/tmp/elsewhere/dibbla") {
		t.Error("expected /tmp/elsewhere/dibbla to not be under GOBIN")
	}
}

func TestDetect_DevVersionAlwaysGoInstall(t *testing.T) {
	m, _ := detectWith("dev", stubRunner{})
	if m != MethodGoInstall {
		t.Errorf("expected MethodGoInstall for dev, got %v", m)
	}
}

// Detect on the host running tests classifies whatever path
// `os.Executable` resolves to. We can't make strong assertions about
// that path, but we can verify Detect doesn't panic and returns a
// non-empty path.
func TestDetect_NonDevReturnsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows path resolution differs")
	}
	_, path := detectWith("v1.0.0", stubRunner{})
	if path == "" {
		t.Error("expected non-empty path")
	}
}
