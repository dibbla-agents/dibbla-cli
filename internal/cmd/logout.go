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
	Long: `Removes the API token and optional API URL stored by "dibbla login" from
the OS credential store and from the user-level credentials file
(used as a fallback on hosts where no keyring service is available).`,
	Run: runLogout,
}

func runLogout(cmd *cobra.Command, args []string) {
	// Keychain removal is best-effort: on hosts without libsecret the
	// keyring lookup itself errors, but we don't want logout to fail
	// just because there was nothing in the keyring to remove. Treat
	// "keyring unavailable" the same as "nothing to delete."
	if err := credential.DeleteToken(); err != nil && !credential.IsKeyringUnavailable(err) {
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	_ = credential.DeleteAPIURL()
	// Always remove the user-level file too — it's where credentials
	// land on hosts without a keyring, and keeping it would leave the
	// user "logged in" by virtue of the fallback read path in config.Load.
	if err := credential.DeleteTokenFile(); err != nil {
		fmt.Printf("%s Warning: failed to remove %s: %v\n",
			platform.Icon("⚠", "[!]"), credential.TokenFilePath(), err)
	}
	fmt.Printf("%s Logged out; credentials removed from keychain and %s\n",
		platform.Icon("✅", "[OK]"), credential.TokenFilePath())
}
