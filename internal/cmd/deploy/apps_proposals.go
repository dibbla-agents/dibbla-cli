package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	proposalsListJSON   bool
	proposalsShowJSON   bool
	proposalsShowDiff   bool
	proposalsActionJSON bool
	proposalsActionYes  bool
)

var appsProposalsCmd = &cobra.Command{
	Use:   "proposals",
	Short: "Review and decide deployment proposals",
	Long: `List and inspect the server-owned deployment proposal read model.

Approval eligibility is never derived by the CLI: show renders the decision
object returned by the API, and approve/deny/retry let the server enforce the
captured governance and maintenance four-eyes policy.`,
}

var appsProposalsListCmd = &cobra.Command{
	Use:   "list <alias>",
	Short: "List an app's deployment proposals",
	Args:  cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runAppsProposalsListCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], proposalsListJSON))
	},
}

var appsProposalsShowCmd = &cobra.Command{
	Use:   "show <alias> <proposal-id>",
	Short: "Show a proposal, its decision state and optional exact diff",
	Long: `Show the proposal read model including actor, status, audit events and
the API-owned decision capability. --diff additionally fetches the exact
server-generated diff between the two immutable proposal revisions.

With --json alone, stdout is the proposal API document. With --diff --json,
stdout is one schema-versioned document containing the two unmodified API
documents under proposal and diff.`,
	Args: cobra.ExactArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runAppsProposalsShowCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], args[1], proposalsShowDiff, proposalsShowJSON))
	},
}

func proposalActionCommand(action string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action + " <alias> <proposal-id>",
		Short: title(action) + " a deployment proposal",
		Long: fmt.Sprintf(`%s a proposal through the server-owned decision endpoint.

The CLI does not calculate eligibility. The API enforces role, author
separation, proposal status and immutable revision conflicts. Exit codes are
3 auth/permission, 5 validation, 6 conflict and 1 technical failure.`, title(action)),
		Args: cobra.ExactArgs(2),
		Run: func(_ *cobra.Command, args []string) {
			cfg := config.Load()
			requireToken(cfg)
			os.Exit(runAppsProposalActionCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken,
				args[0], args[1], action, proposalsActionYes, proposalsActionJSON, askConfirm))
		},
	}
	cmd.Flags().BoolVarP(&proposalsActionYes, "yes", "y", false, "Confirm non-interactively")
	cmd.Flags().BoolVar(&proposalsActionJSON, "json", false, "Print the raw API proposal document")
	return cmd
}

var (
	appsProposalsApproveCmd = proposalActionCommand("approve")
	appsProposalsDenyCmd    = proposalActionCommand("deny")
	appsProposalsRetryCmd   = proposalActionCommand("retry")
)

func init() {
	appsProposalsListCmd.Flags().BoolVar(&proposalsListJSON, "json", false, "Print the raw API document")
	appsProposalsShowCmd.Flags().BoolVar(&proposalsShowDiff, "diff", false, "Include the exact server-generated diff and evidence")
	appsProposalsShowCmd.Flags().BoolVar(&proposalsShowJSON, "json", false, "Print machine-readable JSON")
	appsProposalsCmd.AddCommand(appsProposalsListCmd, appsProposalsShowCmd, appsProposalsApproveCmd, appsProposalsDenyCmd, appsProposalsRetryCmd)
}

func runAppsProposalsListCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	page, raw, err := apps.ListProposals(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "proposals list", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if len(page.Proposals) == 0 {
		fmt.Fprintf(stdout, "%s no proposals for %s\n", platform.Icon("🔀", "[PROPOSALS]"), alias)
		return 0
	}
	fmt.Fprintf(stdout, "%s proposals for %s\n", platform.Icon("🔀", "[PROPOSALS]"), alias)
	fmt.Fprintf(stdout, "   %-24s %-18s %-22s %-20s %s\n", "ID", "STATUS", "SOURCE", "ACTOR", "TITLE")
	for i := range page.Proposals {
		p := &page.Proposals[i]
		actor := p.AuthorName
		if actor == "" {
			actor = p.AuthorEmail
		}
		if actor == "" {
			actor = p.AuthorID
		}
		fmt.Fprintf(stdout, "   %-24s %-18s %-22s %-20s %s\n", p.ID, p.Status, orDash(p.Source), actor, p.Title)
	}
	return 0
}

func runAppsProposalsShowCore(stdout, stderr io.Writer, apiURL, apiToken, alias, proposalID string, withDiff, jsonOut bool) int {
	if code := validateProposalTarget(stderr, alias, proposalID); code != 0 {
		return code
	}
	proposal, proposalRaw, err := apps.GetProposal(apiURL, apiToken, alias, proposalID)
	if err != nil {
		return reportAppError(stderr, "proposals show", alias, err)
	}
	var diff *apps.ProposalDiffResponse
	var diffRaw []byte
	if withDiff {
		diff, diffRaw, err = apps.GetProposalDiff(apiURL, apiToken, alias, proposalID)
		if err != nil {
			return reportAppError(stderr, "proposal diff", alias, err)
		}
	}
	if jsonOut {
		if !withDiff {
			fmt.Fprintln(stdout, string(proposalRaw))
			return 0
		}
		writeJSONDocument(stdout, map[string]any{
			"schema_version": 1,
			"type":           "proposal_review",
			"proposal":       prettyRawObject(proposalRaw),
			"diff":           prettyRawObject(diffRaw),
		})
		return 0
	}
	actor := proposal.AuthorName
	if actor == "" {
		actor = proposal.AuthorEmail
	}
	if actor == "" {
		actor = proposal.AuthorID
	}
	fmt.Fprintf(stdout, "%s %s — %s\n", platform.Icon("🔀", "[PROPOSAL]"), proposal.ID, proposal.Title)
	fmt.Fprintf(stdout, "   status:   %s\n", proposal.Status)
	fmt.Fprintf(stdout, "   source:   %s\n", orDash(proposal.Source))
	fmt.Fprintf(stdout, "   actor:    %s (%s)\n", actor, proposal.AuthorID)
	fmt.Fprintf(stdout, "   revision: %.12s → %.12s\n", proposal.BaseSHA, proposal.HeadSHA)
	fmt.Fprintf(stdout, "   decision: %s", proposal.Decision.Reason)
	if proposal.Decision.Message != "" {
		fmt.Fprintf(stdout, " — %s", proposal.Decision.Message)
	}
	fmt.Fprintln(stdout)
	if proposal.DecisionBy != "" {
		fmt.Fprintf(stdout, "   decided by: %s\n", proposal.DecisionBy)
	}
	if proposal.Description != "" {
		fmt.Fprintf(stdout, "\n%s\n", proposal.Description)
	}
	if !withDiff {
		return 0
	}
	renderProposalEvidence(stdout, diff)
	fmt.Fprintf(stdout, "\nDiff: %d file(s), +%d -%d", diff.Diff.TotalFiles, diff.Diff.Additions, diff.Diff.Deletions)
	if diff.Diff.Truncated {
		fmt.Fprint(stdout, " (truncated by server)")
	}
	fmt.Fprintln(stdout)
	for _, file := range diff.Diff.Files {
		fmt.Fprintf(stdout, "\n--- %s (+%d -%d)", file.Path, file.Additions, file.Deletions)
		if file.Binary {
			fmt.Fprint(stdout, " [binary]")
		}
		if file.Truncated {
			fmt.Fprint(stdout, " [truncated]")
		}
		fmt.Fprintln(stdout)
		if file.Patch != "" {
			fmt.Fprint(stdout, file.Patch)
			if !strings.HasSuffix(file.Patch, "\n") {
				fmt.Fprintln(stdout)
			}
		}
	}
	return 0
}

func renderProposalEvidence(stdout io.Writer, diff *apps.ProposalDiffResponse) {
	if diff.Risk != "" {
		fmt.Fprintf(stdout, "   risk:     %s\n", diff.Risk)
	}
	if len(diff.Evidence) == 0 {
		return
	}
	encoded, err := json.MarshalIndent(diff.Evidence, "", "  ")
	if err == nil {
		fmt.Fprintf(stdout, "   evidence:\n%s\n", encoded)
	}
}

func runAppsProposalActionCore(stdout, stderr io.Writer, apiURL, apiToken, alias, proposalID, action string, yes, jsonOut bool, confirm func(string) (bool, error)) int {
	if code := validateProposalTarget(stderr, alias, proposalID); code != 0 {
		return code
	}
	if action != "approve" && action != "deny" && action != "retry" {
		fmt.Fprintf(stderr, "unsupported proposal action %q\n", action)
		return 5
	}
	if !yes {
		ok, err := confirm(fmt.Sprintf("%s proposal '%s' for '%s'?", title(action), proposalID, alias))
		if err != nil {
			return refuseUnconfirmable(stderr, action+" proposal '"+proposalID+"'")
		}
		if !ok {
			fmt.Fprintln(stdout, "No decision made.")
			return 0
		}
	}
	proposal, raw, err := apps.DecideProposal(apiURL, apiToken, alias, proposalID, action)
	if err != nil {
		return reportAppError(stderr, "proposal "+action, alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "%s proposal %s: %s", platform.Icon("✓", "[OK]"), proposal.ID, proposal.Status)
	if proposal.DecisionBy != "" {
		fmt.Fprintf(stdout, " by %s", proposal.DecisionBy)
	}
	fmt.Fprintln(stdout)
	return 0
}

func validateProposalTarget(stderr io.Writer, alias, proposalID string) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	if !apps.ProposalIDRe.MatchString(proposalID) {
		fmt.Fprintf(stderr, "%s proposal id %q is invalid\n", platform.Icon("❌", "[X]"), proposalID)
		return 5
	}
	return 0
}
