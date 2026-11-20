package deploy

import (
	"fmt"
	"io"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

// The read view of an app's variables and secrets (DIB-1837) is printed the
// same way by apps get, env pull and secrets list -d, so the three agree on
// every name's kind, scope, origin and live state.

// envState is an entry's state in one word or phrase: whether the running
// app has it, or another entry of the same name wins.
func envState(e apps.EnvEntry) string {
	switch {
	case !e.Effective:
		return "not used"
	case !e.Live:
		return "restart needed"
	default:
		return "live"
	}
}

// envScope is an entry's scope, with an inherited global marked as such.
func envScope(e apps.EnvEntry) string {
	if e.Scope == "global" {
		return "global (inherited)"
	}
	return e.Scope
}

// envLabel is "secret, app, origin console, live" — everything but the value.
func envLabel(e apps.EnvEntry) string {
	return e.Kind + ", " + envDetail(e)
}

// envDetail is envLabel without the kind: "app, origin console, live".
func envDetail(e apps.EnvEntry) string {
	parts := []string{envScope(e)}
	if e.Origin != "" {
		parts = append(parts, "origin "+e.Origin)
	}
	parts = append(parts, envState(e))
	return strings.Join(parts, ", ")
}

// envValue is what the value column shows: a variable's value, never a
// secret's.
func envValue(e apps.EnvEntry) string {
	switch {
	case e.Kind == "secret":
		return "(secret — never shown)"
	case e.ValueHidden || e.Value == nil:
		return "(hidden: the name is also a secret's)"
	default:
		return strings.ReplaceAll(*e.Value, "\n", `\n`)
	}
}

// printEnvTable prints entries as a table, indented, with the warnings and a
// restart hint under it.
func printEnvTable(w io.Writer, indent, alias string, doc *apps.EnvResponse, entries []apps.EnvEntry, withValues bool) {
	nameW := len("NAME")
	for _, e := range entries {
		if len(e.Name) > nameW {
			nameW = len(e.Name)
		}
	}
	row := func(cols ...string) {
		line := fmt.Sprintf("%s%-*s  %-9s %-19s %-9s %-15s", indent, nameW, cols[0], cols[1], cols[2], cols[3], cols[4])
		if withValues {
			line += " " + cols[5]
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	valueHeader := ""
	if withValues {
		valueHeader = "VALUE"
	}
	row("NAME", "KIND", "SCOPE", "ORIGIN", "STATE", valueHeader)
	restart := false
	for _, e := range entries {
		origin := e.Origin
		if origin == "" {
			origin = "-"
		}
		row(e.Name, e.Kind, envScope(e), origin, envState(e), envValue(e))
		if e.Effective && !e.Live {
			restart = true
		}
	}
	printEnvNotes(w, indent, alias, doc, entries, restart)
}

// printEnvNotes prints the warnings for the names in entries and, when a
// saved value is not live, how to apply it.
func printEnvNotes(w io.Writer, indent, alias string, doc *apps.EnvResponse, entries []apps.EnvEntry, restart bool) {
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, warn := range doc.Warnings {
		if names[warn.Name] {
			fmt.Fprintf(w, "%s! %s\n", indent, warn.Message)
		}
	}
	if restart {
		fmt.Fprintf(w, "%sSome values were saved after the app started; restart it to apply them: dibbla apps restart %s\n", indent, alias)
	}
}
