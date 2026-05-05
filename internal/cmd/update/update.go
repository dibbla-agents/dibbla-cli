package update

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/dibbla-agents/dibbla-cli/internal/update"
)

var (
	flagCheck   bool
	flagForce   bool
	flagYes     bool
	flagVersion string
)

// version is the running build's version. Wired in from cmd.Version via
// the Register hook so this package doesn't import internal/cmd (cycle).
var version = "dev"

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update dibbla to the latest version",
	Long: `Update dibbla to the latest released version.

How it behaves depends on how dibbla was installed:
  - Homebrew / apt / rpm / scoop: prints the right upgrade command
    for your package manager, but doesn't run it.
  - Script install (~/.local/bin or %LOCALAPPDATA%): downloads the
    latest release archive, verifies its SHA-256 against checksums.txt,
    and atomically replaces the binary.
  - Development build (` + "`Version == \"dev\"`" + `): refuses to self-replace.

Examples:
  dibbla update
  dibbla update --check
  dibbla update --version v1.4.2
  dibbla update --force --yes`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&flagCheck, "check", false, "Only check whether a newer version is available; don't install")
	updateCmd.Flags().BoolVar(&flagForce, "force", false, "Reinstall even if already on the requested version")
	updateCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	updateCmd.Flags().StringVar(&flagVersion, "version", "", "Install a specific release tag (e.g. v1.2.3) instead of latest")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	method, binPath := Detect(version)

	// --check is informational only: works regardless of install method.
	if flagCheck {
		return runCheck()
	}

	// Pinned version skips the latest fetch but still goes through
	// the same release/checksum flow.
	tag := strings.TrimSpace(flagVersion)
	rel, err := update.FetchRelease(version, tag)
	if err != nil {
		return fmt.Errorf("fetch release info: %w", err)
	}
	if rel == nil || rel.TagName == "" {
		return fmt.Errorf("no release found")
	}

	if !flagForce && tag == "" && !needsUpdate(version, rel.TagName) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s dibbla is up to date (%s)\n",
			platform.Icon("✦", "*"), version)
		return nil
	}

	switch method {
	case MethodGoInstall:
		return fmt.Errorf("this looks like a development build (version %q); rebuild from source instead of running `dibbla update`", version)

	case MethodHomebrew, MethodDebian, MethodRPM, MethodScoop, MethodChocolatey:
		fmt.Fprintf(cmd.OutOrStdout(),
			"%s dibbla was installed via %s.\n  Run: %s\n",
			platform.Icon("✦", "*"), method, UpgradeCommand(method))
		return nil

	case MethodSystemDir:
		return fmt.Errorf("dibbla is installed at %s but no package manager owns it; "+
			"replace it manually or reinstall via the script at https://install.dibbla.com", binPath)

	case MethodScript:
		return runSelfReplace(cmd, rel, binPath)

	default:
		return fmt.Errorf("could not determine how dibbla was installed (path: %s); "+
			"reinstall from https://install.dibbla.com or your package manager", binPath)
	}
}

func runCheck() error {
	rel, err := update.FetchRelease(version, "")
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	if rel == nil || rel.TagName == "" {
		return fmt.Errorf("no release found")
	}

	cur, err := semver.NewVersion(strings.TrimPrefix(version, "v"))
	if err != nil {
		// Dev build or unparseable: report the latest and exit clean.
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", version, rel.TagName)
		return nil
	}
	latest, err := semver.NewVersion(strings.TrimPrefix(rel.TagName, "v"))
	if err != nil {
		return fmt.Errorf("parse latest version %q: %w", rel.TagName, err)
	}

	switch {
	case latest.GreaterThan(cur):
		fmt.Fprintf(os.Stdout, "newer version available: v%s → v%s\n", cur, latest)
		os.Exit(1)
	case cur.GreaterThan(latest):
		fmt.Fprintf(os.Stdout, "ahead of latest release (current %s, latest %s)\n", version, rel.TagName)
	default:
		fmt.Fprintf(os.Stdout, "up to date (%s)\n", version)
	}
	return nil
}

func runSelfReplace(cmd *cobra.Command, rel *update.Release, binPath string) error {
	if !CanWrite(binPath) {
		return fmt.Errorf("can't write to %s; re-run with sudo or reinstall via https://install.dibbla.com", binPath)
	}

	if !flagYes {
		msg := fmt.Sprintf("Replace %s with %s?", binPath, rel.TagName)
		if !prompt.AskConfirm(msg) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s Downloading %s for %s/%s...\n",
		platform.Icon("⬇", "->"), AssetName(rel.TagName), runtime.GOOS, runtime.GOARCH)

	if err := SelfReplace(rel, binPath, version); err != nil {
		return err
	}

	// Suppress the next "newer version available" notice from the
	// background notifier — we just upgraded.
	update.WriteCachedLatest(rel.TagName)

	fmt.Fprintf(cmd.OutOrStdout(), "%s dibbla %s → %s installed.\n",
		platform.Icon("✓", "[OK]"), version, rel.TagName)
	return nil
}

func needsUpdate(current, latest string) bool {
	cur, err := semver.NewVersion(strings.TrimPrefix(current, "v"))
	if err != nil {
		return true
	}
	lat, err := semver.NewVersion(strings.TrimPrefix(latest, "v"))
	if err != nil {
		return false
	}
	return lat.GreaterThan(cur)
}
