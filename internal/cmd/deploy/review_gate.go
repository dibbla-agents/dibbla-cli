package deploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxSubtitleBytes is the bundler's hard cap on the handbook subtitle.
// The server rejects longer values; we reproduce only this hard rule
// locally (not the softer ≤70-char target) so the gate never blocks a
// deploy the server would have accepted.
const maxSubtitleBytes = 140

type missingArtifact struct {
	what string
	hint string
}

// checkReviewArtifacts returns the list of pre-deploy artifacts missing
// at root. A deploy is gated on REVIEW.md plus a user handbook
// (docs/index.md or APP.md) carrying a valid subtitle: frontmatter.
// Returning an empty slice means the gate passes.
func checkReviewArtifacts(root string) []missingArtifact {
	var missing []missingArtifact
	if !fileExists(filepath.Join(root, "REVIEW.md")) {
		missing = append(missing, missingArtifact{
			what: "REVIEW.md (pre-deploy guardrails report)",
			hint: "Run the pre-deploy checklist and write the report — see .claude/skills/dibbla/guardrails.md",
		})
	}

	// Prefer docs/index.md, then APP.md — the same order the server uses
	// to pick the landing page it reads the subtitle from.
	handbook := ""
	if p := filepath.Join(root, "docs", "index.md"); fileExists(p) {
		handbook = p
	} else if p := filepath.Join(root, "APP.md"); fileExists(p) {
		handbook = p
	}

	if handbook == "" {
		missing = append(missing, missingArtifact{
			what: "user handbook (docs/index.md or APP.md)",
			hint: "Add end-user docs at docs/index.md or APP.md — see .claude/skills/dibbla/user-docs.md",
		})
	} else if what := subtitleProblem(handbook); what != "" {
		missing = append(missing, missingArtifact{
			what: what,
			hint: "Write a one-sentence user-facing subtitle: in the handbook frontmatter (≤140 bytes, no placeholders) — see .claude/skills/dibbla/user-docs.md",
		})
	}
	return missing
}

// subtitleProblem validates the subtitle: frontmatter on the handbook at
// path. It returns an empty string when the subtitle is valid, otherwise a
// short noun phrase describing what is missing/invalid (rendered after
// "missing " by writeReviewGateError). Only the bundler's hard rejections
// are reproduced; subjective quality rules stay human-judgment guardrails.
func subtitleProblem(path string) string {
	name := filepath.Base(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("readable subtitle: frontmatter on %s (%v)", name, err)
	}
	fm, ok := extractFrontmatter(data)
	if !ok {
		return fmt.Sprintf("subtitle: frontmatter on %s (no YAML frontmatter block found)", name)
	}
	var meta struct {
		Subtitle string `yaml:"subtitle"`
	}
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return fmt.Sprintf("valid subtitle: frontmatter on %s (could not parse YAML)", name)
	}
	sub := strings.TrimSpace(meta.Subtitle)
	switch {
	case sub == "":
		return fmt.Sprintf("subtitle: value in %s frontmatter", name)
	case len(sub) > maxSubtitleBytes:
		return fmt.Sprintf("valid subtitle: in %s (it is %d bytes; the hard cap is %d)", name, len(sub), maxSubtitleBytes)
	case hasPlaceholder(sub):
		return fmt.Sprintf("real subtitle: in %s (it still contains placeholder text)", name)
	}
	return ""
}

// extractFrontmatter returns the bytes between the leading "---" fence and
// its closing "---" line. ok is false when data does not open with a
// frontmatter block.
func extractFrontmatter(data []byte) (fm []byte, ok bool) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return []byte(strings.Join(lines[1:i], "\n")), true
		}
	}
	return nil, false
}

// hasPlaceholder reports whether s still holds scaffolding placeholder text
// that should have been replaced with a real subtitle.
func hasPlaceholder(s string) bool {
	low := strings.ToLower(s)
	for _, tok := range []string{"tbd", "todo", "fixme", "<one short", "{{", "placeholder"} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
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
