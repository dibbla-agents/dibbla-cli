package cmd

import (
	"fmt"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored API credentials",
	Long:  `Removes the API token and optional API URL stored by "dibbla login" from the OS credential store.`,
	Run:   runLogout,
}

func runLogout(cmd *cobra.Command, args []string) {
	if err := credential.DeleteToken(); err != nil {
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	_ = credential.DeleteAPIURL()
	fmt.Printf("%s Logged out; credentials removed from keychain\n", platform.Icon("✅", "[OK]"))
}
