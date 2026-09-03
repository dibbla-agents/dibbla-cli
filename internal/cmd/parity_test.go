package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/platformcontract"
	"github.com/spf13/cobra"
)

// The parity gate for dibbla-cli (P-0035 Part F, DIB-676).
//
// THE RULE. What a signed-in human can do with this CLI, an OAuth grant with
// the right scope can do through /platform. An exception is allowed, but it is
// a decision written into the contract — a local-only row with a reason that
// states a technical impossibility and a source that cites the code, or a
// not-yet-available row with an owner and a work item — never a forgetting.
//
// WHAT THIS FILE CAN PROVE, AND WHAT IT CANNOT. It walks the real command tree
// and asks the vendored contract whether each command is covered. It cannot
// see tools/list, so it cannot tell whether a row that says remote-* is
// actually reachable; app-hosting-service owns that half, against the server
// that serves it. Neither half is worth much alone, and that is the point of
// putting each where its evidence lives.
//
// GRANULARITY IS THE LEAF. A command with subcommands dispatches; a command
// without them does something. Adding a subcommand to a group therefore adds a
// command to cover, and the group itself stops needing one. `dibbla feedback`
// is the exception the contract names explicitly — it both dispatches and
// sends feedback — and it is listed by the row that covers its subcommands.

// cliLeafCommands is every leaf of the real command tree, as full paths
// without the `dibbla` root. Built from rootCmd rather than from a list, so a
// command added anywhere in the tree arrives here without anyone remembering.
func cliLeafCommands() []string {
	var out []string
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + c.Name())
		if len(c.Commands()) == 0 {
			if c.Runnable() {
				out = append(out, path)
			}
			return
		}
		for _, sub := range c.Commands() {
			walk(sub, path)
		}
	}
	for _, c := range rootCmd.Commands() {
		walk(c, "")
	}
	sort.Strings(out)
	return out
}

func TestEveryCLICommandIsCoveredByAContractRow(t *testing.T) {
	var uncovered []string
	for _, path := range cliLeafCommands() {
		if _, ok := platformcontract.CoversCLICommand(path); !ok {
			uncovered = append(uncovered, path)
		}
	}
	if len(uncovered) == 0 {
		return
	}

	parity := platformcontract.ParityRules()
	t.Fatalf(`%d command(s) have no capability row in the platform contract:

  %s

The rule this gate exists for: %s

Decide, do not delete the test. For each command above, add a row to
architecture/docs/contract/v1/capabilities.json and re-vendor with
scripts/vendor-contract.sh:

  * it already works through /platform  -> a remote-read / remote-write /
    remote-destructive / remote-async row naming the mcp_tool that delivers it.
    app-hosting-service's own gate will then prove the tool is really in
    tools/list.
  * it can never work through /platform -> a local-only row whose reason is a
    TECHNICAL impossibility — it reads the caller's filesystem, needs a TTY,
    handles a local credential — and whose source cites the code. Caution is
    not a reason.
  * it should work through /platform but nobody has built it -> a
    not-yet-available row with an eventual_state, an owner and the work item
    that closes it. An owner with no item is a parking space.

Several commands may share one row: the unit is the capability, not the
command.`,
		len(uncovered), strings.Join(uncovered, "\n  "), parity.Rule)
}

// TestContractNamesNoCommandThisCLIDoesNotHave is the other direction. A row
// naming a command that no longer exists is coverage of nothing, and it is how
// a rename quietly turns a real gap into a green gate.
func TestContractNamesNoCommandThisCLIDoesNotHave(t *testing.T) {
	actual := map[string]bool{}
	for _, path := range cliLeafCommands() {
		actual[path] = true
	}
	// A group command is named by the contract only where it does work of its
	// own beside dispatching; the tree walk above deliberately does not
	// collect those, so accept them here.
	var walkAll func(c *cobra.Command, prefix string)
	walkAll = func(c *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + c.Name())
		if c.Runnable() {
			actual[path] = true
		}
		for _, sub := range c.Commands() {
			walkAll(sub, path)
		}
	}
	for _, c := range rootCmd.Commands() {
		walkAll(c, "")
	}

	var stale []string
	for _, c := range platformcontract.Capabilities() {
		for _, cmd := range c.CLICommands {
			if !actual[cmd] {
				stale = append(stale, fmt.Sprintf("%q (named by %s)", cmd, c.ID))
			}
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("the contract names %d command(s) this CLI does not have:\n  %s\n\n"+
			"A row pointing at a command that was renamed or removed covers nothing, and the command it "+
			"was renamed to is then uncovered without anyone noticing. Update the row in "+
			"architecture/docs/contract/v1/capabilities.json and re-vendor.",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestLocalOnlyRowsStateATechnicalReason is the discipline behind the
// exception. A local-only row is the only way a command escapes /platform, so
// the shape of its justification is checked here too — not because the
// canonical verifier does not check it (it does), but because this is the
// repository whose commands the exception is about, and a vendored copy that
// has lost the rule should fall here.
func TestLocalOnlyRowsStateATechnicalReason(t *testing.T) {
	seen := 0
	for _, c := range platformcontract.Capabilities() {
		if c.State != platformcontract.StateLocalOnly {
			continue
		}
		seen++
		switch {
		case len(c.CLICommands) == 0:
			t.Errorf("%s is local-only and names no command; it excuses nothing", c.ID)
		case strings.TrimSpace(c.Reason) == "":
			t.Errorf("%s is local-only and states no reason", c.ID)
		case strings.TrimSpace(c.Source) == "":
			t.Errorf("%s is local-only and cites no code", c.ID)
		}
	}
	if seen == 0 {
		t.Fatal("the vendored contract has no local-only rows at all; this gate would prove nothing")
	}
}

// TestSkillDocsNameTheRealLocalOnlyRows keeps the agent-facing documentation
// honest about the same rule the gate above enforces.
//
// The skill is what an agent reads before deciding whether to reach for a tool
// or tell the user to open a terminal, so a stale exception table there costs
// more than a stale paragraph elsewhere: it teaches the agent to give up on a
// capability that shipped. The table names contract row ids precisely so this
// can be checked rather than remembered.
func TestSkillDocsNameTheRealLocalOnlyRows(t *testing.T) {
	const doc = "../../.claude/skills/dibbla/platform.md"
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("reading %s: %v", doc, err)
	}
	text := string(raw)

	var missing []string
	for _, c := range platformcontract.Capabilities() {
		if c.State != platformcontract.StateLocalOnly {
			continue
		}
		if !strings.Contains(text, c.ID) {
			missing = append(missing, c.ID)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s does not name these local-only rows:\n  %s\n\n"+
			"An agent reads the skill to decide whether a thing is worth trying remotely. A local-only row "+
			"it has never heard of is a capability it will tell the user to open a terminal for. Add them to "+
			"the table in § 13 and re-run `go generate ./...`.",
			doc, strings.Join(missing, "\n  "))
	}

	// The other direction: a row id in the document that the contract no
	// longer has is an exception that was lifted without the skill noticing —
	// the agent keeps refusing something that now works.
	ids := map[string]bool{}
	for _, c := range platformcontract.Capabilities() {
		ids[c.ID] = true
	}
	for _, m := range regexp.MustCompile(`\bcli\.[a-z0-9_.]+`).FindAllString(text, -1) {
		if !ids[m] {
			t.Errorf("%s names %q, which is not a capability in the contract; if the exception was lifted, "+
				"say so in the table instead of leaving the row id behind", doc, m)
		}
	}
}
