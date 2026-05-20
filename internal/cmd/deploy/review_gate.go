package deploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type missingArtifact struct {
	what string
	hint string
}

// checkReviewArtifacts returns the list of pre-deploy artifacts missing
// at root. A deploy is gated on REVIEW.md plus a user handbook
// (docs/index.md or APP.md). Returning an empty slice means the gate
// passes.
func checkReviewArtifacts(root string) []missingArtifact {
	var missing []missingArtifact
	if !fileExists(filepath.Join(root, "REVIEW.md")) {
		missing = append(missing, missingArtifact{
			what: "REVIEW.md (pre-deploy guardrails report)",
			hint: "Run the pre-deploy checklist and write the report — see .claude/skills/dibbla/guardrails.md",
		})
	}
	hasDocsIndex := fileExists(filepath.Join(root, "docs", "index.md"))
	hasAppMd := fileExists(filepath.Join(root, "APP.md"))
	if !hasDocsIndex && !hasAppMd {
		missing = append(missing, missingArtifact{
			what: "user handbook (docs/index.md or APP.md)",
			hint: "Add end-user docs at docs/index.md or APP.md — see .claude/skills/dibbla/user-docs.md",
		})
	}
	return missing
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// writeReviewGateError prints a human-readable explanation of why the
// gate blocked the deploy and how to proceed. The wording is shared
// between the CLI runtime and tests.
func writeReviewGateError(w io.Writer, missing []missingArtifact) {
	fmt.Fprintln(w, "✗ deploy blocked: pre-deploy review incomplete")
	fmt.Fprintln(w)
	for _, m := range missing {
		fmt.Fprintf(w, "  • missing %s\n", m.what)
		fmt.Fprintf(w, "    %s\n", m.hint)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If you've intentionally chosen to skip the review (e.g. a one-line")
	fmt.Fprintln(w, "typo fix from a human), re-run with --skip-review.")
}
