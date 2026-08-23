// Package contextcmd implements the `dibbla context` command group (P-0011):
// named login targets, so a user can stay logged in to several Dibbla API
// servers at once and switch the active one with a single command.
package contextcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// opts holds the command group's flag values. They live on a per-registration
// struct rather than in package-level vars so that building the command tree is
// a pure function of its inputs: Register can be called more than once in one
// process (every test does) without cobra panicking on a duplicate flag, and
// two trees never share mutable state.
type opts struct {
	listJSON bool
	rmForce  bool
}

// Register adds the context command group to the root command.
func Register(root *cobra.Command) {
	root.AddCommand(newContextCmd())
}

func newContextCmd() *cobra.Command {
	o := &opts{}

	contextCmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"contexts", "ctx"},
		Short:   "Show and switch the API server the CLI talks to",
		Long: `Show and switch the API server the CLI talks to.

A context is a named login target — an API URL, the token for it, and the
organization pinned on that server. Several can exist at once, so logging in to
a customer instance no longer destroys your production login.

The list of contexts is a plain, editable file at ~/.config/dibbla/config.yaml.
It holds no secrets: tokens stay in the OS keyring, or in a per-context
credentials file on hosts with no keyring service.

Precedence, highest first:
  --context <name>        just this invocation
  DIBBLA_CONTEXT          this shell
  dibbla context use ...  stored, until you change it
  (none)                  ` + config.DefaultAPIURL + `

DIBBLA_API_TOKEN and DIBBLA_API_URL — from the shell or from ./.env — still win
over all of the above, so CI and one-shot scripted calls are unaffected.

Note that "dibbla update" is deliberately not context-aware: it upgrades this
machine's CLI, which is not a per-server thing.`,
		Args: cobra.NoArgs,
		RunE: o.runList,
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the configured contexts",
		Args:    cobra.NoArgs,
		RunE:    o.runList,
	}

	useCmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Talk to the given context's server from now on",
		Long: `Talk to the given context's server from now on.

The selection persists across invocations and directories until you change it.
It also repoints the legacy ~/.config/dibbla/credentials.env at the newly
selected context, so a dibbla binary older than named contexts — and any script
that sources that file — follows the switch instead of silently staying on the
previous server.`,
		Args: cobra.ExactArgs(1),
		RunE: o.runUse,
	}

	currentCmd := &cobra.Command{
		Use:   "current",
		Short: "Print the active context's name",
		Long: `Print the active context's name.

Respects --context and DIBBLA_CONTEXT, so this answers "what would the next
command actually use" rather than "what is stored".`,
		Args: cobra.NoArgs,
		RunE: o.runCurrent,
	}

	renameCmd := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a context",
		Args:  cobra.ExactArgs(2),
		RunE:  o.runRename,
	}

	rmCmd := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a context and the token stored for it",
		Long: `Remove a context and the token stored for it.

There is no "clear" here, unlike "dibbla org clear": an org can be unpinned and
fall back to the account's default, but a context has no meaningful unpinned
state — removing one destroys a stored credential. Removing the context you are
currently using is refused without --force, because silently dropping you onto
` + config.DefaultAPIURL + ` is the one outcome a multi-server tool must not have.`,
		Args: cobra.ExactArgs(1),
		RunE: o.runRm,
	}

	listCmd.Flags().BoolVar(&o.listJSON, "json", false, "Emit machine-readable JSON instead of a table")
	contextCmd.Flags().BoolVar(&o.listJSON, "json", false, "Emit machine-readable JSON instead of a table")
	rmCmd.Flags().BoolVar(&o.rmForce, "force", false, "Remove even when it is the context in use")
	contextCmd.AddCommand(listCmd, useCmd, currentCmd, renameCmd, rmCmd)
	return contextCmd
}

// load reads config.yaml after running the one-time legacy import, so a user
// who has just upgraded sees their existing login here as a context rather
// than an empty list.
func load() (*contextcfg.Config, error) {
	_, _, _ = config.Migrate()
	return contextcfg.Load()
}

type contextRow struct {
	Name    string `json:"name"`
	APIURL  string `json:"api_url"`
	Org     string `json:"org,omitempty"`
	OrgName string `json:"org_name,omitempty"`
	// LoggedIn reports whether a token is actually stored for this context. A
	// context whose token was removed still appears in config.yaml, and a list
	// that did not say so would show a login that is not there.
	LoggedIn bool `json:"logged_in"`
	Current  bool `json:"current"`
}

func rows(cfg *contextcfg.Config, active string) []contextRow {
	out := make([]contextRow, 0, len(cfg.Contexts))
	for _, name := range cfg.Names() {
		c := cfg.Contexts[name]
		out = append(out, contextRow{
			Name:     name,
			APIURL:   c.APIURL,
			Org:      c.Org,
			OrgName:  c.OrgName,
			LoggedIn: config.ResolveContextNamed(name).Token != "",
			Current:  name == active,
		})
	}
	return out
}

func (o *opts) runList(cmd *cobra.Command, args []string) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	active := activeName(cfg)
	rs := rows(cfg, active)

	if o.listJSON {
		// Always an array, never null: `dibbla context list --json | jq` on a
		// machine with no contexts should yield an empty list, not a nil.
		body, err := json.MarshalIndent(rs, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(body))
		return nil
	}

	if len(rs) == 0 {
		fmt.Fprintln(out, "No contexts configured. Run `dibbla login` to add one.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tNAME\tAPI URL\tORG\t")
	for _, r := range rs {
		marker := ""
		if r.Current {
			marker = platform.Icon("→", "*")
		}
		org := r.OrgName
		if org == "" {
			org = r.Org
		}
		note := ""
		if !r.LoggedIn {
			note = "no token stored"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", marker, r.Name, r.APIURL, org, note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if active == "" {
		fmt.Fprintf(out, "\nNo context selected — commands fall back to %s.\n", config.DefaultAPIURL)
		fmt.Fprintln(out, "Select one with `dibbla context use <name>`.")
	}
	return nil
}

func (o *opts) runUse(cmd *cobra.Command, args []string) error {
	name := args[0]
	cfg, err := load()
	if err != nil {
		return err
	}
	ctx, ok := cfg.Get(name)
	if !ok {
		return unknownContext(cfg, name)
	}
	cfg.Current = name
	if err := cfg.Save(); err != nil {
		return err
	}
	// Repoint the legacy single-slot storage at the newly selected context.
	// Without this a pre-context binary, and every script that sources
	// credentials.env, would keep talking to the previous server.
	config.SyncLegacyMirror()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s Now using context %s (%s)\n", platform.Icon("✅", "[OK]"), name, ctx.APIURL)
	if config.ResolveContextNamed(name).Token == "" {
		fmt.Fprintf(out, "%s No token is stored for %s — run `dibbla login --context %s`.\n",
			platform.Icon("⚠", "[!]"), name, name)
	}
	warnShadowed(out)
	return nil
}

func (o *opts) runCurrent(cmd *cobra.Command, args []string) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	name := activeName(cfg)
	if name == "" {
		return fmt.Errorf("no context selected — commands fall back to %s; select one with `dibbla context use <name>`", config.DefaultAPIURL)
	}
	fmt.Fprintln(cmd.OutOrStdout(), name)
	return nil
}

func (o *opts) runRename(cmd *cobra.Command, args []string) error {
	oldName, newName := args[0], args[1]
	if oldName == newName {
		return nil
	}
	if !contextcfg.ValidName(newName) {
		return fmt.Errorf("%q is not a usable context name: use letters, digits, dot, dash or underscore (it becomes a filename and a keyring key)", newName)
	}
	cfg, err := load()
	if err != nil {
		return err
	}
	ctx, ok := cfg.Get(oldName)
	if !ok {
		return unknownContext(cfg, oldName)
	}
	if _, exists := cfg.Get(newName); exists {
		return fmt.Errorf("context %q already exists", newName)
	}

	// Re-key the token before touching config.yaml, and write the new key
	// before deleting the old one. If the delete fails the token is readable
	// under both names, which is recoverable; the other order loses it.
	if err := moveToken(oldName, newName); err != nil {
		return fmt.Errorf("move the stored token: %w", err)
	}

	cfg.Set(newName, ctx)
	cfg.Delete(oldName)
	if cfg.Current == "" {
		// Delete cleared it because the renamed context was the current one.
		cfg.Current = newName
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	config.SyncLegacyMirror()
	fmt.Fprintf(cmd.OutOrStdout(), "%s Renamed context %s to %s\n", platform.Icon("✅", "[OK]"), oldName, newName)
	return nil
}

func (o *opts) runRm(cmd *cobra.Command, args []string) error {
	name := args[0]
	cfg, err := load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Get(name); !ok {
		return unknownContext(cfg, name)
	}
	if cfg.Current == name && !o.rmForce {
		return fmt.Errorf("%s is the context in use; switch with `dibbla context use <other>` first, or pass --force to remove it and be left with none", name)
	}

	// The stored token goes first, and a failure to remove it aborts before
	// config.yaml is touched.
	//
	// The other order looks equivalent and is not. If config.yaml were written
	// first and the delete then failed, the token would still be in the OS
	// keyring with nothing left referring to it — an orphaned secret that no
	// dibbla command can reach or remove, on a machine whose owner has just
	// been told the context was removed. This way the worst partial state is a
	// context whose token is gone, which `dibbla context list` reports as "no
	// token stored" and a second `rm` finishes cleaning up.
	if err := credential.DeleteContextToken(name); err != nil && !credential.IsKeyringUnavailable(err) {
		return fmt.Errorf("remove the stored token for %s: %w", name, err)
	}
	if err := credential.DeleteContextTokenFile(name); err != nil {
		return fmt.Errorf("remove the credentials file for %s: %w", name, err)
	}

	cfg.Delete(name)
	if err := cfg.Save(); err != nil {
		return err
	}
	config.SyncLegacyMirror()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s Removed context %s\n", platform.Icon("✅", "[OK]"), name)
	if cfg.Current == "" {
		fmt.Fprintf(out, "%s No context selected — commands fall back to %s until you run `dibbla context use <name>`.\n",
			platform.Icon("ℹ", "[i]"), config.DefaultAPIURL)
	}
	return nil
}

// activeName reports which context the next command would use, honouring
// --context and DIBBLA_CONTEXT rather than only the stored selection.
func activeName(cfg *contextcfg.Config) string {
	if n := strings.TrimSpace(config.ContextOverride); n != "" {
		return n
	}
	if n := strings.TrimSpace(os.Getenv("DIBBLA_CONTEXT")); n != "" {
		return n
	}
	return strings.TrimSpace(cfg.Current)
}

// unknownContext builds an error that lists what does exist. A bare "unknown
// context" leaves the user to go and look, and the list is three lines away.
func unknownContext(cfg *contextcfg.Config, name string) error {
	names := cfg.Names()
	if len(names) == 0 {
		return fmt.Errorf("no such context %q — none are configured; run `dibbla login` to add one", name)
	}
	return fmt.Errorf("no such context %q — configured: %s", name, strings.Join(names, ", "))
}

// warnShadowed points out env vars that override what was just selected, at the
// moment of selection rather than at the next confusing 401.
func warnShadowed(out io.Writer) {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_CONTEXT")); v != "" {
		fmt.Fprintf(out, "%s DIBBLA_CONTEXT is set to %s in this shell and takes precedence over what was just stored.\n",
			platform.Icon("⚠", "[!]"), v)
	}
	if strings.TrimSpace(os.Getenv("DIBBLA_API_TOKEN")) != "" {
		fmt.Fprintf(out, "%s DIBBLA_API_TOKEN is set in this shell and wins over every context.\n",
			platform.Icon("⚠", "[!]"))
	}
}

// moveToken re-keys a context's token from old to new, in whichever store holds
// it, writing before deleting. A no-op when no token is stored.
func moveToken(oldName, newName string) error {
	if t, err := credential.GetContextToken(oldName); err == nil && t != "" {
		if err := credential.SetContextToken(newName, t); err != nil {
			return err
		}
		return credential.DeleteContextToken(oldName)
	}
	if t, url, err := credential.GetContextTokenFile(oldName); err == nil && t != "" {
		if err := credential.SetContextTokenFile(newName, t, url); err != nil {
			return err
		}
		return credential.DeleteContextTokenFile(oldName)
	}
	return nil
}
