package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "dibbla",
	Short:   "Dibbla CLI - scaffold and manage Dibbla projects",
	Version: Version,
	Long: `Dibbla CLI helps you create and manage Dibbla worker projects.

Get started:
  dibbla create go-worker my-project`,
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("dibbla version %s\n", Version))
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

