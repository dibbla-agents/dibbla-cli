package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dibbla",
	Short: "Dibbla CLI - scaffold and manage Dibbla projects",
	Long: `Dibbla CLI helps you create and manage Dibbla worker projects.

Get started:
  dibbla create go-worker my-project`,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

