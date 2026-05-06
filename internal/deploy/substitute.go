package deploy

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// substVarRe matches ${VAR} and ${VAR:-default}. Var names accept a
// leading letter or underscore followed by letters, digits, or underscores
// — the standard shell-env identifier shape. Case-sensitive: shell vars
// like `${HOME}` are common, and we don't want false positives on free
// text that happens to contain `${...}`.
//
// The regex deliberately does NOT match `$VAR` (no braces); the brace form
// is the only supported one, mirroring the deploy-api server's substitution
// regex and avoiding accidental hits on Compose-style `$$VAR` literals.
var substVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// reservedShellVarPrefix names the prefix that the CLI MUST NOT substitute.
// Variables starting with DIBBLA_ are server-side discovery contract slots
// (DIBBLA_ALIAS, DIBBLA_ENV, DIBBLA_SERVICE_NAME, DIBBLA_SVC_<NAME>_HOST/_PORT/_URL,
// etc.) that the deploy-api populates at render time. Substituting them
// client-side would either drop the placeholder (when not in shell env) or
// override the server's value (when set). Either case breaks the discovery
// contract.
const reservedShellVarPrefix = "DIBBLA_"

// SubstituteShellVars expands `${VAR}` and `${VAR:-default}` references in
// data using the provided env lookup. Returns the substituted bytes and any
// error encountered.
//
// Rules (mirroring docker-compose's behavior, with one safety addition):
//   - `${VAR}`: if VAR is in env → substitute with its value
//   - `${VAR}`: if VAR is NOT in env and the name starts with DIBBLA_ →
//     leave the placeholder intact (server resolves it later)
//   - `${VAR}`: if VAR is NOT in env and no default → return an error
//     naming the unresolved var (this is the "unset shell var" case)
//   - `${VAR:-default}`: if VAR is in env → substitute with its value;
//     otherwise substitute with `default` (which may be empty string)
//   - `$$` (Compose escape) → unescape to literal `$` so users can write
//     a literal `${VAR}` placeholder in their YAML by typing `$${VAR}`
//
// The function operates on raw bytes (text-level), so YAML formatting,
// comments, and anchors are preserved byte-for-byte except where
// substitutions happen.
func SubstituteShellVars(data []byte, env func(string) (string, bool)) ([]byte, error) {
	// Handle the $$ escape FIRST: replace `$$` with a placeholder that
	// substVarRe can't match, do the regex pass, then restore. This avoids
	// the regex matching `$${VAR}` as `${VAR}` after the dollar.
	const escMarker = "\x00DIBBLA_ESC\x00"
	preEsc := strings.ReplaceAll(string(data), "$$", escMarker)

	var firstErr error
	out := substVarRe.ReplaceAllStringFunc(preEsc, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := substVarRe.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := strings.Contains(match, ":-")
		def := groups[2]

		// Reserved server-side prefix: never substitute, regardless of
		// whether the shell env happens to have a matching var. Doing
		// otherwise would let a stray DIBBLA_ALIAS in the user's shell
		// shadow the server's value.
		if strings.HasPrefix(name, reservedShellVarPrefix) {
			return match
		}
		val, found := env(name)
		if found {
			return val
		}
		if hasDefault {
			return def
		}
		// Otherwise: hard error so the user catches typos early instead of
		// silently shipping an empty string into their pod env.
		firstErr = fmt.Errorf("unresolved shell variable %s; set %s in your shell or provide a default with ${%s:-fallback}", match, name, name)
		return match
	})
	if firstErr != nil {
		return nil, firstErr
	}

	// Restore $$ escapes to a literal `$`.
	out = strings.ReplaceAll(out, escMarker, "$")
	return []byte(out), nil
}

// SubstituteShellVarsFromOSEnv is the convenience wrapper that uses the
// process's actual shell environment via os.LookupEnv.
func SubstituteShellVarsFromOSEnv(data []byte) ([]byte, error) {
	return SubstituteShellVars(data, os.LookupEnv)
}
