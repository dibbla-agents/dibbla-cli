package cmd

import (
	"fmt"
	"io"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	logoutContext string
	logoutAll     bool
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of the context in use, or of every context",
	Long: `Log out of the context in use.

Removes that context's stored token from the OS credential store and from its
credentials file, and removes the context itself. Other contexts are left alone,
so logging out of a customer instance does not log you out of production.

  dibbla logout                    the context in use
  dibbla logout --context <name>   a named context, whichever is in use
  dibbla logout --all              every context, and the legacy credentials

Also clears the organization selected with "dibbla org use" for whatever is
removed, since that selection belongs to the credentials it was made with.`,
	Args: cobra.NoArgs,
	RunE: runLogout,
}

func init() {
	// NOTE: this shadows the root persistent --context flag, deliberately, in
	// the same way login's does. Theirs names the context to READ; this one
	// names the context to LOG OUT OF.
	logoutCmd.Flags().StringVar(&logoutContext, "context", "", "Log out of this context instead of the one in use")
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Log out of every context and remove the context list")
}

func runLogout(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	store, err := contextcfg.Load()
	if err != nil {
		return err
	}

	if logoutAll {
		return logoutEverything(out, store)
	}

	name := logoutContext
	if name == "" {
		name = store.Current
	}
	if name == "" {
		// Nothing selected. This is not an error: on a machine that never
		// logged in, or one already logged out, "log me out" has succeeded.
		// Clear the legacy slot anyway, because a pre-context binary may have
		// written one that nothing else would now remove.
		config.ClearLegacyMirror()
		fmt.Fprintf(out, "%s Not logged in to any context; nothing to do.\n", platform.Icon("✅", "[OK]"))
		return nil
	}
	if _, ok := store.Get(name); !ok {
		return fmt.Errorf("no such context %q — `dibbla context list` shows the configured ones", name)
	}

	if err := forgetContextCredentials(name); err != nil {
		return err
	}
	store.Delete(name)
	if err := store.Save(); err != nil {
		return err
	}
	config.SyncLegacyMirror()

	fmt.Fprintf(out, "%s Logged out of context %s\n", platform.Icon("✅", "[OK]"), name)
	if remaining := store.Names(); len(remaining) > 0 {
		fmt.Fprintf(out, "   Still logged in to: %v\n", remaining)
		if store.Current == "" {
			fmt.Fprintf(out, "%s No context selected — pick one with `dibbla context use <name>`.\n",
				platform.Icon("ℹ", "[i]"))
		}
	}
	return nil
}

func logoutEverything(out io.Writer, store *contextcfg.Config) error {
	names := store.Names()
	for _, name := range names {
		if err := forgetContextCredentials(name); err != nil {
			return err
		}
	}
	// Credentials files can outlive their config.yaml entry — a hand-edited
	// file, or a crash between the two writes. Enumerating the directory as
	// well as the config is what makes --all mean "nothing is left".
	for _, name := range credential.ListContextTokenFiles() {
		if err := credential.DeleteContextTokenFile(name); err != nil {
			return err
		}
	}
	if err := contextcfg.Remove(); err != nil {
		return err
	}
	config.ClearLegacyMirror()

	fmt.Fprintf(out, "%s Logged out of every context (%d) and removed %s\n",
		platform.Icon("✅", "[OK]"), len(names), contextcfg.Path())
	return nil
}

// forgetContextCredentials removes a context's token from both stores.
//
// Best-effort across the two on purpose: a host without a keyring still has the
// file, a host with one still might have a stale file from before the keyring
// worked, and "there was nothing to delete" is the same outcome as "deleted".
// A hard keyring error is NOT swallowed, though — silently reporting a logout
// that did not remove the credential is worse than failing.
func forgetContextCredentials(name string) error {
	if err := credential.DeleteContextToken(name); err != nil && !credential.IsKeyringUnavailable(err) {
		return fmt.Errorf("remove the stored token for %s: %w", name, err)
	}
	if err := credential.DeleteContextTokenFile(name); err != nil {
		return fmt.Errorf("remove the credentials file for %s: %w", name, err)
	}
	return nil
}
