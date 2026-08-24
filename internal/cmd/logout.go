package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/auth"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	logoutContext string
	logoutAll     bool
	logoutLocal   bool
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
	logoutCmd.Flags().BoolVar(&logoutLocal, "local-only", false, "Forget the credential here without ending the session on the server")
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

	endSessionFor(out, store, name)

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
		endSessionFor(out, store, name)
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

// endSessionFor ends a context's server-side CLI session, so logging out means
// the credential stops working rather than merely being forgotten here.
//
// Before DIB-416 logout only dropped the local copy and the token stayed live
// for the rest of its life, which is most of how accounts filled up with
// credentials nobody could account for.
//
// Deliberately best-effort, and deliberately not returning an error. Three
// things can go wrong and none of them should stop a logout:
//
//   - the context has no session id (created by --api-key, or by a CLI older
//     than this) — there is nothing on the server that this owns;
//   - the server is unreachable, or older than the endpoint;
//   - the session is already gone.
//
// A logout that refuses to finish would leave the machine still holding a
// usable credential, which is worse than a session that lingers until it
// expires on its own. What it must not do is fail silently: an unended session
// is reported so it can be dealt with in the web UI.
func endSessionFor(out io.Writer, store *contextcfg.Config, name string) {
	if logoutLocal {
		return
	}
	ctx, ok := store.Get(name)
	if !ok || ctx.SessionID == "" {
		return
	}

	tok, err := credential.GetContextToken(name)
	if err != nil || strings.TrimSpace(tok) == "" {
		tok, _, err = credential.GetContextTokenFile(name)
		if err != nil || strings.TrimSpace(tok) == "" {
			return
		}
	}

	if err := auth.RevokeCLISession(ctx.APIURL, strings.TrimSpace(tok), ctx.SessionID); err != nil {
		fmt.Fprintf(out, "%s Could not end the session for %s on the server: %v\n"+
			"   The local credential is being removed anyway. If that session should not\n"+
			"   outlive this logout, end it from the web UI.\n",
			platform.Icon("⚠", "[!]"), name, err)
	}
}
