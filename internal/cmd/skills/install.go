package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
)

var (
	installUser     bool
	installForce    bool
	installNoAgents bool
)

var installCmd = &cobra.Command{
	Use:   "install <id>",
	Short: "Install a skill into this project (or your home dir)",
	Long: `Install a skill into this project's .claude/skills/<id>/ directory.

By default also writes AGENTS.md and GEMINI.md at the target root, with a
marker-delimited pointer block so that Cursor, Opencode, Codex, Gemini CLI,
and other AGENTS.md-compatible agents see the skill. Use --no-agents to skip
those two files.

Re-running is safe: identical files are no-ops. If a skill file has been
edited locally, the install refuses to overwrite it unless --force is passed.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runInstall,
}

func init() {
	installCmd.Flags().BoolVar(&installUser, "user", false, "Install to $HOME instead of the current directory")
	installCmd.Flags().BoolVar(&installForce, "force", false, "Overwrite existing skill files that differ from the bundled version")
	installCmd.Flags().BoolVar(&installNoAgents, "no-agents", false, "Skip writing AGENTS.md and GEMINI.md")
}

var skillIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func validateSkillID(id string) error {
	if id == "" {
		return fmt.Errorf("skill id is required")
	}
	if !skillIDPattern.MatchString(id) {
		return fmt.Errorf("invalid skill id %q: must match [a-z][a-z0-9-]*", id)
	}
	return nil
}

func resolveTargetRoot(userFlag bool) (string, error) {
	if userFlag {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		return home, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving current directory: %w", err)
	}
	return wd, nil
}

func runInstall(cmd *cobra.Command, args []string) error {
	id := args[0]
	if err := validateSkillID(id); err != nil {
		return err
	}

	entry, err := find(id)
	if err != nil {
		return err
	}

	root, err := resolveTargetRoot(installUser)
	if err != nil {
		return err
	}

	skillFiles, err := entry.files()
	if err != nil {
		return fmt.Errorf("reading embedded skill: %w", err)
	}

	destDir := filepath.Join(root, ".claude", "skills", entry.id)
	if err := writeSkillFiles(skillFiles, destDir, root, installForce); err != nil {
		return err
	}

	cliVersion := cmd.Root().Version
	if cliVersion == "" {
		cliVersion = "dev"
	}

	if !installNoAgents {
		block := renderPointerBlock(entry.id, cliVersion)
		for _, name := range []string{"AGENTS.md", "GEMINI.md"} {
			path := filepath.Join(root, name)
			if err := writeAgentsFile(path, block); err != nil {
				return err
			}
		}
	}

	cliout.Stderr("✓ installed skill %q (dibbla %s) into %s", entry.id, cliVersion, root)
	return nil
}

// writeSkillFiles writes every file in the skill FS under destDir. For each
// file, if the destination already exists with identical bytes it's left alone
// (idempotent, no mtime bump). If it exists with different bytes, we require
// force. Unknown files in destDir are always preserved. displayRoot is used
// only to produce friendlier progress messages; pass destDir to get paths
// relative to the skill dir itself.
func writeSkillFiles(skillFS fs.FS, destDir, displayRoot string, force bool) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", destDir, err)
	}

	return fs.WalkDir(skillFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		destPath := filepath.Join(destDir, path)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		embedded, err := fs.ReadFile(skillFS, path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		if existing, err := os.ReadFile(destPath); err == nil {
			if bytes.Equal(existing, embedded) {
				return nil
			}
			if !force {
				return fmt.Errorf("%s exists with different content (use --force to overwrite)", destPath)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", destPath, err)
		}

		if err := atomicWrite(destPath, embedded, 0o644); err != nil {
			return err
		}
		display := destPath
		if displayRoot != "" {
			if rel, err := filepath.Rel(displayRoot, destPath); err == nil {
				display = rel
			}
		}
		cliout.Stderr("writing %s", display)
		return nil
	})
}

// atomicWrite writes data to path via a sibling temp file + rename. The temp
// file is removed if any step fails so we never leave .dibbla.tmp litter.
// Existing file mode is preserved if the target already exists.
func atomicWrite(path string, data []byte, defaultMode os.FileMode) error {
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	// Resolve symlinks so we rewrite the underlying file, not replace the link.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".dibbla.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return fmt.Errorf("renaming to %s: %w", target, err)
	}
	return nil
}

// writeAgentsFile inserts-or-replaces the pointer block in path. No-op if the
// rendered content equals what's already on disk.
func writeAgentsFile(path, block string) error {
	existing := ""
	if raw, err := os.ReadFile(path); err == nil {
		existing = string(raw)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	rendered := renderAgentsBlock(existing, block)
	if rendered == existing {
		return nil
	}

	if err := atomicWrite(path, []byte(rendered), 0o644); err != nil {
		return err
	}
	cliout.Stderr("updated %s", filepath.Base(path))
	return nil
}

const (
	markerOpen  = "<!-- >>> dibbla skill >>> -->"
	markerClose = "<!-- <<< dibbla skill <<< -->"
)

// renderAgentsBlock is a pure function: it computes the new contents for an
// AGENTS.md / GEMINI.md given the existing contents and the block to embed.
//
// Rules:
//   - Replace in place if both markers are present (open before close).
//   - If only one marker is present, treat as append so we don't eat user
//     content up to a stray marker-like string.
//   - Preserve the input's line-ending style (CRLF vs LF).
//   - Always end with exactly one trailing newline.
func renderAgentsBlock(existing, block string) string {
	eol := "\n"
	if strings.Contains(existing, "\r\n") {
		eol = "\r\n"
	}

	normalizedBlock := normalizeEOL(strings.TrimRight(block, "\r\n"), eol)
	trailing := eol

	if openIdx := strings.Index(existing, markerOpen); openIdx >= 0 {
		closeIdx := strings.Index(existing[openIdx:], markerClose)
		if closeIdx >= 0 {
			// Replace the marked span (markers included).
			before := existing[:openIdx]
			afterStart := openIdx + closeIdx + len(markerClose)
			after := existing[afterStart:]
			after = strings.TrimLeft(after, "\r\n")
			out := before + normalizedBlock
			if after != "" {
				out += eol + eol + after
			}
			return ensureSingleTrailingEOL(out, eol)
		}
	}

	trimmed := strings.TrimSpace(existing)
	if trimmed == "" {
		return normalizedBlock + trailing
	}

	base := strings.TrimRight(existing, "\r\n")
	return base + eol + eol + normalizedBlock + trailing
}

func normalizeEOL(s, eol string) string {
	if eol == "\n" {
		return strings.ReplaceAll(s, "\r\n", "\n")
	}
	// Target is CRLF: strip any existing CRs first so we don't double up.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func ensureSingleTrailingEOL(s, eol string) string {
	s = strings.TrimRight(s, "\r\n")
	return s + eol
}

// renderPointerBlock produces the marker-delimited block for AGENTS.md/GEMINI.md.
func renderPointerBlock(id, cliVersion string) string {
	var b strings.Builder
	b.WriteString(markerOpen + "\n")
	b.WriteString("## Dibbla CLI\n\n")
	b.WriteString("This project uses the Dibbla CLI. Detailed guidance for agents using it lives at:\n\n")
	b.WriteString(fmt.Sprintf("- `.claude/skills/%s/SKILL.md` — entry point (commands, flags, agent guidelines)\n", id))
	b.WriteString(fmt.Sprintf("- `.claude/skills/%s/reference.md` — full command reference\n", id))
	b.WriteString(fmt.Sprintf("- `.claude/skills/%s/examples.md` — example flows\n", id))
	b.WriteString(fmt.Sprintf("- `.claude/skills/%s/guardrails.md` — safety checks\n\n", id))
	b.WriteString(fmt.Sprintf("Installed by `dibbla skills install %s` (CLI %s). Re-run to refresh.\n", id, cliVersion))
	b.WriteString(markerClose)
	return b.String()
}

