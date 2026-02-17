package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags
var Version = "dev"

var skillPrompt bool

var rootCmd = &cobra.Command{
	Use:     "dibbla",
	Short:   "Dibbla CLI - scaffold and manage Dibbla projects",
	Version: Version,
	Long: `Dibbla CLI helps you create and manage Dibbla worker projects.

Get started:
  dibbla create go-worker my-project`,
	Run: func(cmd *cobra.Command, args []string) {
		if skillPrompt {
			data, err := os.ReadFile("SKILL.md")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: could not read SKILL.md: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(data))
		} else {
			cmd.Help()
		}
	},
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("dibbla version %s\n", Version))
	rootCmd.Flags().BoolVar(&skillPrompt, "skill-prompt", false, "Show detailed instructions for LLM-based tools")
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

