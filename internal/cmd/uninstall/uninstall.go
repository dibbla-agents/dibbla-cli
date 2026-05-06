// Package uninstall implements `dibbla uninstall`: a self-removal
// command that deletes CLI-owned config, skill artifacts, and (for
// script installs only) the dibbla binary itself. For binaries owned
// by a package manager it prints the right native uninstall command
// and offers to remove config without touching the binary.
package uninstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	updatecmd "github.com/dibbla-agents/dibbla-cli/internal/cmd/update"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/skills"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/dibbla-agents/dibbla-cli/internal/skillregistry"
	"github.com/dibbla-agents/dibbla-cli/internal/update"
)

var (
	flagYes         bool
	flagDryRun      bool
	flagKeepConfig  bool
	flagKeepSkills  bool
	flagSkillOnly   bool
)

// version is the build-time CLI version, wired in via Register.
var version = "dev"

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove dibbla, its config, and skill files installed by `dibbla skills install`",
	Long: `Uninstall the dibbla CLI from this machine.

What gets removed depends on how dibbla was installed:
  - Homebrew / apt / rpm / scoop / chocolatey: prints the native
    uninstall command (you run it). Optionally cleans dibbla's
    config and skill files first.
  - Script install (~/.local/bin or %LOCALAPPDATA%): self-deletes
    the binary after cleaning config and skill files.
  - Development build (Version == "dev") or unrecognized location:
    refuses to remove the binary; offers to clean config only.

Config cleanup includes:
  - Stored credentials in the OS keychain (token + API URL).
  - Update-notifier state (~/.config/dibbla/state.yml).
  - Templates cache (~/.dibbla/templates-cache.json).
  - The skill-installs registry (~/.dibbla/skill-installs.json).

Skill cleanup uses the registry written by ` + "`dibbla skills install`" + ` to
find every project where the dibbla skill was installed, and removes
the .claude/skills/dibbla/ tree plus the marker block from AGENTS.md
and GEMINI.md at each location.

Examples:
  dibbla uninstall                # interactive prompt; full removal
  dibbla uninstall --yes          # skip the prompt
  dibbla uninstall --dry-run      # show the plan, change nothing
  dibbla uninstall --keep-config  # remove binary, keep credentials
  dibbla uninstall --keep-skills  # don't touch installed skill files
  dibbla uninstall --skill-only   # only remove skill files; leave binary`,
	RunE:         runUninstall,
	SilenceUsage: true,
}

func init() {
	uninstallCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	uninstallCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be removed without changing anything")
	uninstallCmd.Flags().BoolVar(&flagKeepConfig, "keep-config", false, "Don't remove credentials, state, or cache")
	uninstallCmd.Flags().BoolVar(&flagKeepSkills, "keep-skills", false, "Don't touch installed skill files")
	uninstallCmd.Flags().BoolVar(&flagSkillOnly, "skill-only", false, "Only remove installed skill files; leave binary and config alone")
}

// step is one removable artifact in the uninstall plan.
type step struct {
	label   string
	exec    func() error
	skipped bool
	reason  string
}

func runUninstall(cmd *cobra.Command, args []string) error {
	method, binPath := updatecmd.Detect(version)
	plan := buildPlan(method, binPath)

	out := cmd.OutOrStdout()
	printPlan(out, plan, method, binPath)

	if flagDryRun {
		fmt.Fprintln(out, "\n(dry-run: no changes made)")
		return nil
	}

	if !flagYes {
		if !prompt.AskConfirm("Proceed with uninstall?") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	return executePlan(out, plan, method, binPath)
}

// buildPlan composes the ordered list of removal steps based on flags
// and the detected install method. Self-binary removal is always last
// in the slice — see executePlan for why.
func buildPlan(method updatecmd.Method, binPath string) []step {
	var steps []step

	// 1. Skill cleanup (per registry).
	if !flagKeepSkills {
		entries := skillregistry.Entries()
		for _, e := range entries {
			e := e
			steps = append(steps, step{
				label: fmt.Sprintf("skill files at %s (id=%s)", e.Root, e.ID),
				exec: func() error {
					removed, errs := skills.UninstallSkill(e.Root, e.ID)
					_ = removed
					if len(errs) > 0 {
						return joinErrs(errs)
					}
					return skillregistry.Forget(e.ID, e.Root)
				},
			})
		}
	}

	// --skill-only stops here (also won't touch binary).
	if flagSkillOnly {
		return steps
	}

	// 2. CLI-owned config / state.
	if !flagKeepConfig {
		steps = append(steps, step{
			label: "stored credentials (OS keychain)",
			exec: func() error {
				if err := credential.DeleteToken(); err != nil {
					return err
				}
				return credential.DeleteAPIURL()
			},
		})

		if statePath := update.StateFilePath(); statePath != "" {
			steps = append(steps, step{
				label: statePath,
				exec:  func() error { return removeIfExists(statePath) },
			})
			steps = append(steps, step{
				label: filepath.Dir(statePath) + " (if empty)",
				exec:  func() error { return removeDirIfEmpty(filepath.Dir(statePath)) },
			})
		}

		if dibblaDir := homeDibblaDir(); dibblaDir != "" {
			steps = append(steps, step{
				label: dibblaDir + " (templates cache, skill registry, ...)",
				exec:  func() error { return os.RemoveAll(dibblaDir) },
			})
		}
	}

	// 3. Binary removal — last, because once it's gone the user has no
	//    `dibbla` to retry with if a later step fails.
	switch method {
	case updatecmd.MethodScript:
		steps = append(steps, step{
			label: "binary at " + binPath,
			exec: func() error {
				if !updatecmd.CanWrite(binPath) {
					return fmt.Errorf("can't write to %s; re-run with sudo or remove manually", binPath)
				}
				return selfDeleteBinary(binPath)
			},
		})
	case updatecmd.MethodGoInstall:
		steps = append(steps, step{
			label:   "binary at " + binPath,
			skipped: true,
			reason:  "go-install build; remove via `go clean -i github.com/dibbla-agents/dibbla-cli/...` or delete manually",
		})
	case updatecmd.MethodSystemDir:
		steps = append(steps, step{
			label:   "binary at " + binPath,
			skipped: true,
			reason:  "no package manager owns this path; delete manually if desired",
		})
	case updatecmd.MethodHomebrew, updatecmd.MethodDebian, updatecmd.MethodRPM,
		updatecmd.MethodScoop, updatecmd.MethodChocolatey:
		steps = append(steps, step{
			label:   "binary at " + binPath,
			skipped: true,
			reason:  fmt.Sprintf("managed by %s; run: %s", method, updatecmd.UninstallCommand(method)),
		})
	default:
		steps = append(steps, step{
			label:   "binary at " + binPath,
			skipped: true,
			reason:  "install method unknown; delete manually if desired",
		})
	}

	return steps
}

func printPlan(w io.Writer, steps []step, method updatecmd.Method, binPath string) {
	icon := platform.Icon("✦", "*")
	fmt.Fprintf(w, "%s dibbla uninstall plan (install method: %s)\n", icon, method)

	if len(steps) == 0 {
		fmt.Fprintln(w, "  (nothing to do)")
		return
	}

	for _, s := range steps {
		if s.skipped {
			fmt.Fprintf(w, "  - SKIP: %s — %s\n", s.label, s.reason)
		} else {
			fmt.Fprintf(w, "  - remove: %s\n", s.label)
		}
	}
}

func executePlan(w io.Writer, steps []step, method updatecmd.Method, binPath string) error {
	icon := platform.Icon("✓", "[OK]")
	warn := platform.Icon("⚠", "[!]")

	for _, s := range steps {
		if s.skipped {
			fmt.Fprintf(w, "%s skipped: %s — %s\n", warn, s.label, s.reason)
			continue
		}
		if err := s.exec(); err != nil {
			fmt.Fprintf(w, "%s %s: %v\n", warn, s.label, err)
			// Binary removal is the only fatal step. Everything else
			// continues so we at least clean partial state.
			if s.label == "binary at "+binPath {
				return err
			}
			continue
		}
		fmt.Fprintf(w, "%s removed: %s\n", icon, s.label)
	}

	switch method {
	case updatecmd.MethodScript:
		fmt.Fprintf(w, "\n%s dibbla has been uninstalled.\n", icon)
		if runtime.GOOS == "windows" {
			fmt.Fprintln(w, "  (binary will be removed momentarily after this process exits)")
		}
	case updatecmd.MethodHomebrew, updatecmd.MethodDebian, updatecmd.MethodRPM,
		updatecmd.MethodScoop, updatecmd.MethodChocolatey:
		fmt.Fprintf(w, "\n%s Config and skill files removed.\n", icon)
		fmt.Fprintf(w, "  To remove the binary, run: %s\n", updatecmd.UninstallCommand(method))
	default:
		fmt.Fprintf(w, "\n%s Config and skill files removed (binary left in place).\n", icon)
	}
	return nil
}

// removeIfExists removes the file at path; missing-file is not an error.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removeDirIfEmpty removes the directory at path only if it contains
// no entries. A non-empty directory or missing directory is a no-op.
func removeDirIfEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// homeDibblaDir returns the path to ~/.dibbla, or "" if the user home
// can't be resolved (in which case we skip cleaning it).
func homeDibblaDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dibbla")
}

func joinErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	msg := errs[0].Error()
	for _, e := range errs[1:] {
		msg += "; " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}

