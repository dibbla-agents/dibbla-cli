package skills

import "github.com/spf13/cobra"

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install skills that teach AI coding agents how to use the Dibbla CLI",
	Long: `Install skills that teach AI coding agents how to use the Dibbla CLI.

Skills are bundled with this dibbla binary — no network required. Each skill
writes files that Claude Code, Cursor, Gemini CLI, Opencode, Codex, and other
AGENTS.md-compatible agents read automatically.

Run 'dibbla skills list' to see what's available.`,
}
