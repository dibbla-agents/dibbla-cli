package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// ContextOverride holds the value of the global --context flag (P-0011).
//
// It is bound directly to the root persistent flag rather than routed through
// a PersistentPreRun, for the same reason --org is: `wf` sets its own
// PersistentPreRunE and cobra runs only the nearest hook in the chain, so a
// root hook would silently not run for `dibbla wf ...`. Cobra parses flags
// before any Run, so every reader — including the lazily-resolving org
// transport in internal/orgctx — sees the final value.
var ContextOverride string

// TokenStore says where a context's token was found, for `dibbla status`.
type TokenStore string

const (
	TokenStoreNone    TokenStore = "none"
	TokenStoreKeyring TokenStore = "keyring"
	TokenStoreFile    TokenStore = "credentials file"
)

// Resolved is the outcome of resolving the active context: which context,
// what it points at, and where its token came from.
//
// Err is set when the context layer cannot answer the question at all — a
// malformed config.yaml, or a context named explicitly with --context /
// DIBBLA_CONTEXT that does not exist. Both are user-facing configuration
// errors, and both must be reported rather than absorbed: silently falling
// back to the production default is how a multi-server tool sends a command to
// the wrong server.
type Resolved struct {
	Name       string
	Explicit   bool // the name came from --context or DIBBLA_CONTEXT, not from `current:`
	APIURL     string
	Token      string
	OrgID      string
	OrgName    string
	TokenStore TokenStore
	Count      int // total contexts configured
	Err        error
}

// activeContextName resolves which context a command should use, in
// precedence order: the --context flag, then DIBBLA_CONTEXT, then the
// `current:` entry in config.yaml. explicit reports whether the answer came
// from one of the first two, which is what makes "you named a context that
// does not exist" distinguishable from "nothing is configured yet".
func activeContextName(cfg *contextcfg.Config) (name string, explicit bool) {
	if n := strings.TrimSpace(ContextOverride); n != "" {
		return n, true
	}
	if n := strings.TrimSpace(os.Getenv("DIBBLA_CONTEXT")); n != "" {
		return n, true
	}
	return strings.TrimSpace(cfg.Current), false
}

// ResolveContext returns the active context and its credentials, after running
// the one-time legacy migration.
//
// Exported because `dibbla status` and the `dibbla context` commands need the
// same answer Load() computed, and the alternative — each re-deriving it — is
// exactly the drift this proposal's tests exist to prevent.
func ResolveContext() Resolved {
	migrateIfNeeded()

	cfg, err := contextcfg.Load()
	if err != nil {
		return Resolved{TokenStore: TokenStoreNone, Err: err}
	}

	r := Resolved{TokenStore: TokenStoreNone, Count: len(cfg.Contexts)}
	r.Name, r.Explicit = activeContextName(cfg)
	if r.Name == "" {
		return r
	}

	ctx, ok := cfg.Get(r.Name)
	if !ok {
		if r.Explicit {
			r.Err = fmt.Errorf("no such context %q — `dibbla context list` shows the configured ones", r.Name)
		} else {
			// `current:` names a context that is not in the file. The file has
			// been hand-edited into an inconsistent state; say so rather than
			// acting as though nothing were selected.
			r.Err = fmt.Errorf("%s selects context %q, which it does not define", contextcfg.Path(), r.Name)
		}
		return r
	}

	r.APIURL = strings.TrimSuffix(strings.TrimSpace(ctx.APIURL), "/")
	r.OrgID = strings.TrimSpace(ctx.Org)
	r.OrgName = strings.TrimSpace(ctx.OrgName)

	// Keyring first (one read, so one OS prompt at most), then the
	// per-context credentials file. Same order the single-slot code used,
	// now keyed per context.
	if t, terr := credential.GetContextToken(r.Name); terr == nil && t != "" {
		r.Token, r.TokenStore = t, TokenStoreKeyring
		return r
	}
	if t, _, ferr := credential.GetContextTokenFile(r.Name); ferr == nil && t != "" {
		r.Token, r.TokenStore = t, TokenStoreFile
		return r
	}
	return r
}

// --- Migration ---------------------------------------------------------------

// migrateIfNeeded runs the legacy import. It is called on every ResolveContext
// rather than memoised behind a sync.Once, deliberately.
//
// A sync.Once here looks like an optimization and is a trap: it caches "already
// attempted" across a whole process, which is correct for a CLI invocation that
// only ever sees one config directory, and wrong for any test binary — the
// second test to need migration would silently get the first test's answer, and
// a migration that never ran is indistinguishable from one that succeeded. It
// cost a real failure in internal/cmd/mcp before being removed.
//
// The guard that actually matters is inside Migrate: one os.Stat once
// config.yaml exists. Before that the cost is the same keyring read Load()
// performed unconditionally before contexts existed, so this is not a
// regression — and after migration it is one keyring read fewer, because a
// per-context token is a single key rather than the old token+url pair.
func migrateIfNeeded() {
	_, _, _ = Migrate()
}

// Migrate performs a one-time, idempotent import of a legacy
// single-credential login into a named context. It is a no-op once
// config.yaml exists, and a no-op when there is no legacy credential to
// import — so a fresh install never creates an empty config file.
//
// Ordering matters and is deliberate: the per-context token is written BEFORE
// anything else, so a crash mid-migration leaves the token readable under both
// names rather than under neither. The derived name is a pure function of the
// stored URL, so a re-run after a crash lands on the same context and the
// operation is idempotent.
//
// The legacy slot is NOT deleted, and that is a considered departure from the
// proposal, which said to clean it up. Two reasons, one of them a bug the
// cleanup would have caused:
//
//  1. On a keyring-less host the legacy credentials.env holds the org pin
//     (DIBBLA_ORG_ID / DIBBLA_ORG_NAME) as well as the token, and the only
//     available "delete" is os.Remove of the whole file. Removing it during
//     migration destroys the org pin — silently, and only on the hosts least
//     able to notice.
//  2. Keeping it turns the legacy slot into a mirror of whichever context is
//     current (see MirrorActiveContext). That is what lets a binary predating
//     contexts, and every shipped script that sources credentials.env, keep
//     working after the upgrade — which the proposal requires but its own
//     cleanup step would have broken on the keyring path too, since a
//     downgraded binary reads the legacy keyring key.
//
// One consequence, stated rather than discovered later: if a user upgrades,
// then downgrades and logs in again with the old binary, that login writes the
// legacy slot but no context, and the next upgraded run keeps using the
// migrated context because config.yaml already exists. Re-running
// `dibbla login` fixes it.
func Migrate() (name string, migrated bool, err error) {
	if contextcfg.Exists() {
		return "", false, nil
	}

	// Find a legacy credential: keyring first, then the file fallback.
	legacyToken, legacyURL := "", ""
	legacyOrgID, legacyOrgName := "", ""
	fromFile := false
	if t, u, gerr := credential.GetCredentials(); gerr == nil && t != "" {
		legacyToken, legacyURL = t, u
		legacyOrgID, legacyOrgName, _ = credential.GetOrg()
	} else if t, u, ferr := credential.GetTokenFile(); ferr == nil && t != "" {
		legacyToken, legacyURL, fromFile = t, u, true
		legacyOrgID, legacyOrgName, _ = credential.GetOrgFile()
	}
	if legacyToken == "" {
		// Nothing to migrate. Do not create an empty config file: a fresh
		// install should leave no trace until the user logs in.
		return "", false, nil
	}

	effectiveURL := strings.TrimSuffix(strings.TrimSpace(legacyURL), "/")
	if effectiveURL == "" {
		effectiveURL = DefaultAPIURL
	}
	name = contextcfg.DeriveName(effectiveURL, DefaultAPIURL)

	// Write the per-context token before writing config.yaml, so the file
	// never claims a context whose token has not landed yet.
	if fromFile {
		if werr := credential.SetContextTokenFile(name, legacyToken, effectiveURL); werr != nil {
			return name, false, werr
		}
	} else {
		if werr := credential.SetContextToken(name, legacyToken); werr != nil {
			return name, false, werr
		}
	}

	cfg := &contextcfg.Config{
		Current: name,
		Contexts: map[string]contextcfg.Context{
			name: {
				APIURL: effectiveURL,
				// The machine-wide org pin becomes this context's pin. On a
				// single-server machine — which is every machine before this
				// change — that is exactly what it already meant.
				Org:     strings.TrimSpace(legacyOrgID),
				OrgName: strings.TrimSpace(legacyOrgName),
			},
		},
	}
	if serr := cfg.Save(); serr != nil {
		return name, false, serr
	}
	return name, true, nil
}

// --- The legacy mirror -------------------------------------------------------

// MirrorActiveContext copies a context's credentials into the legacy
// single-slot storage, so that anything still reading the old location sees
// whichever context is current.
//
// What reads the old location: a `dibbla` binary from before this change (users
// upgrade at different times, and `dibbla update` is a separate action), and
// the shipped demo-seeding flows that source ~/.config/dibbla/credentials.env
// directly. The proposal's rule is "the legacy file is whatever context is
// current"; this generalizes it to the keyring too, because a downgraded binary
// on a keyring host reads the legacy keyring key and would otherwise find
// nothing.
//
// The mirror writes into the same store the context's own token lives in, so
// no host gains a plaintext copy of a token it did not already have. On a
// keyring host the token is not written to disk; on a keyring-less host the
// legacy file already existed.
//
// Errors are returned but callers generally warn rather than fail: the context
// itself is stored correctly and the mirror is a compatibility shim.
func MirrorActiveContext(name string) error {
	r := ResolveContextNamed(name)
	if r.Err != nil {
		return r.Err
	}
	if r.Token == "" {
		return nil
	}

	switch r.TokenStore {
	case TokenStoreFile:
		if err := credential.SetTokenFile(r.Token, legacyURLValue(r.APIURL)); err != nil {
			return err
		}
		return credential.SetOrgFile(r.OrgID, r.OrgName)
	default:
		if err := credential.SetToken(r.Token); err != nil {
			return err
		}
		if r.APIURL != "" && r.APIURL != DefaultAPIURL {
			if err := credential.SetAPIURL(r.APIURL); err != nil {
				return err
			}
		} else {
			_ = credential.DeleteAPIURL()
		}
		if r.OrgID == "" {
			return credential.DeleteOrg()
		}
		return credential.SetOrg(r.OrgID, r.OrgName)
	}
}

// legacyURLValue mirrors the old convention: the default endpoint is stored as
// an empty value, which config.Load treated as "no override".
func legacyURLValue(apiURL string) string {
	if apiURL == DefaultAPIURL {
		return ""
	}
	return apiURL
}

// ClearLegacyMirror empties the legacy single-slot storage in both stores.
// Called when the last context goes away, so the CLI does not keep reporting a
// login through the compatibility shim after `dibbla logout`.
func ClearLegacyMirror() {
	_ = credential.DeleteToken()
	_ = credential.DeleteAPIURL()
	_ = credential.DeleteOrg()
	_ = credential.DeleteOrgFile()
	_ = credential.DeleteTokenFile()
}

// SyncLegacyMirror points the legacy slot at whatever context is current, or
// clears it when there is none. This is the single call every command that
// changes the current context makes, so the rule lives in one place instead of
// being re-derived at each call site.
func SyncLegacyMirror() {
	cfg, err := contextcfg.Load()
	if err != nil || strings.TrimSpace(cfg.Current) == "" {
		ClearLegacyMirror()
		return
	}
	_ = MirrorActiveContext(cfg.Current)
}

// ResolveContextNamed resolves one specific context by name, ignoring
// --context, DIBBLA_CONTEXT and `current:`. Used by the mirror and by
// `dibbla context list`, which must report every context rather than the
// active one.
func ResolveContextNamed(name string) Resolved {
	cfg, err := contextcfg.Load()
	if err != nil {
		return Resolved{TokenStore: TokenStoreNone, Err: err}
	}
	r := Resolved{Name: name, TokenStore: TokenStoreNone, Count: len(cfg.Contexts)}
	ctx, ok := cfg.Get(name)
	if !ok {
		r.Err = fmt.Errorf("no such context %q", name)
		return r
	}
	r.APIURL = strings.TrimSuffix(strings.TrimSpace(ctx.APIURL), "/")
	r.OrgID = strings.TrimSpace(ctx.Org)
	r.OrgName = strings.TrimSpace(ctx.OrgName)
	if t, terr := credential.GetContextToken(name); terr == nil && t != "" {
		r.Token, r.TokenStore = t, TokenStoreKeyring
		return r
	}
	if t, _, ferr := credential.GetContextTokenFile(name); ferr == nil && t != "" {
		r.Token, r.TokenStore = t, TokenStoreFile
		return r
	}
	return r
}
