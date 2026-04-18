package template

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/cmd/run"
	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
)

var (
	installForce   bool
	installRefresh bool
)

var installCmd = &cobra.Command{
	Use:   "install <id> [<dir>]",
	Short: "Install a template into a new directory",
	Long: `Install a template into a new directory.

Looks up <id> in the hosted manifest, creates <dir> (default: the template's
template_path from the manifest — e.g. expense-reporter-template-1), changes
into it, and runs the bootstrap yaml. The bootstrap clones the template and
invokes its setup pipeline.

Refuses to install if <dir> already exists, unless --force is passed.`,
	Args:         cobra.RangeArgs(1, 2),
	SilenceUsage: true,
	RunE:         runInstall,
}

func init() {
	installCmd.Flags().BoolVar(&installForce, "force", false, "Overwrite the destination directory if it already exists")
	installCmd.Flags().BoolVar(&installRefresh, "refresh", false, "Force re-fetch of the manifest before install")
}

func runInstall(cmd *cobra.Command, args []string) error {
	id := args[0]

	m, err := resolveManifest(installRefresh, false)
	if err != nil {
		return err
	}
	tmpl := m.FindByID(id)
	if tmpl == nil {
		return fmt.Errorf("unknown template %q (run 'dibbla template list' to see available templates)", id)
	}

	destRel := tmpl.ID
	if len(args) == 2 {
		destRel = args[1]
	}
	dest, err := filepath.Abs(destRel)
	if err != nil {
		return fmt.Errorf("resolving destination path: %w", err)
	}

	if _, err := os.Stat(dest); err == nil {
		if !installForce {
			return fmt.Errorf("destination %q already exists (use --force to reuse)", destRel)
		}
		cliout.Stderr("note: reusing existing directory %s (--force)", dest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dest, err)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating destination %s: %w", dest, err)
	}
	if err := os.Chdir(dest); err != nil {
		return fmt.Errorf("chdir into %s: %w", dest, err)
	}

	cliout.Stderr("installing template %q into %s", tmpl.ID, dest)
	return run.ExecuteTask(cmd.Context(), tmpl.BootstrapURL, run.TaskOpts{})
}
