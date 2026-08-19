package cmd

import (
	_ "embed"
	"fmt"

	"github.com/dibbla-agents/dibbla-cli/internal/cmd/admincmd"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/aigateway"
	deploycmd "github.com/dibbla-agents/dibbla-cli/internal/cmd/deploy"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/initcmd"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/logs"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/manifestcmd"
	mcpcmd "github.com/dibbla-agents/dibbla-cli/internal/cmd/mcp"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/preview"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/run"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/skills"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/template"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/uninstall"
	updatecmd "github.com/dibbla-agents/dibbla-cli/internal/cmd/update"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/wf"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/orgctx"
	"github.com/dibbla-agents/dibbla-cli/internal/update"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags
var Version = "dev"

var skillPrompt bool
var checkInBackground = update.CheckInBackground
var printNotice = update.PrintNotice

//go:embed skill.md
var skillPromptContent string

//go:generate sh -c "cp ../../SKILL.md skill.md"

var rootCmd = &cobra.Command{
	Use:     "dibbla",
	Short:   "Dibbla CLI - scaffold and manage Dibbla projects",
	Version: Version,
	Long: `Dibbla CLI helps you create and manage Dibbla worker projects.

Get started:
  dibbla init`,
	Run: func(cmd *cobra.Command, args []string) {
		if skillPrompt {
			fmt.Print(skillPromptContent)
		} else {
			cmd.Help()
		}
	},
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("dibbla version %s\n", Version))
	rootCmd.Flags().BoolVar(&skillPrompt, "skill-prompt", false, "Show detailed instructions for LLM-based tools")
	// Bound straight to the config package rather than routed through a
	// PersistentPreRun: `wf` sets its own PersistentPreRunE, and cobra runs
	// only the nearest hook in the chain, so a root hook would silently not
	// run for `dibbla wf ...`. Cobra parses flags before any Run, so every
	// consumer of config.FlagOrgID reads it after it has been populated.
	rootCmd.PersistentFlags().StringVar(&config.FlagOrgID, "org", "",
		"Organization id to act as for this command; overrides DIBBLA_ORG_ID and the stored selection")
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(orgCmd)
	rootCmd.AddCommand(feedbackCmd)
	deploycmd.Register(rootCmd)
	wf.Register(rootCmd)
	run.Register(rootCmd)
	logs.Register(rootCmd)
	template.Register(rootCmd)
	skills.Register(rootCmd)
	updatecmd.Register(rootCmd, Version)
	uninstall.Register(rootCmd, Version)
	initcmd.Register(rootCmd)
	manifestcmd.Register(rootCmd)
	preview.Register(rootCmd)
	admincmd.Register(rootCmd)
	aigateway.Register(rootCmd)
	mcpcmd.Register(rootCmd)
}

// Execute runs the root command.
//
// We load ./.env once here, before dispatching any subcommand, so that env
// vars like DIBBLA_API_TOKEN and DIBBLA_API_URL are visible to every command
// via os.Getenv — including dibbla login, which otherwise wouldn't see them.
// godotenv.Load() does not overwrite vars already present in the shell env, so
// explicit shell exports still win over .env. Centralizing it here avoids
// having each command remember to call godotenv.Load() individually.
func Execute() error {
	_ = godotenv.Load()
	// Must come after godotenv.Load, since the org may be set via
	// DIBBLA_ORG_ID in ./.env. Resolution inside the transport is lazy, so
	// installing it here costs nothing for commands that never call the API.
	orgctx.Install()
	ch := checkInBackground(Version)
	err := rootCmd.Execute()
	if ch != nil {
		select {
		case info := <-ch:
			if info != nil {
				printNotice(info, Version)
			}
		default:
		}
	}
	return err
}
