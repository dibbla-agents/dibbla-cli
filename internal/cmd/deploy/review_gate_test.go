package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validHandbook is a handbook body with a well-formed subtitle: frontmatter.
const validHandbook = "---\nsubtitle: Track expenses and get reimbursed faster.\n---\n\n# Welcome\n"

func TestCheckReviewArtifacts(t *testing.T) {
	type setup struct {
		reviewMD bool
		docsIdx  bool
		appMD    bool
	}
	cases := []struct {
		name    string
		setup   setup
		wantLen int
	}{
		{"all present (docs/index.md)", setup{reviewMD: true, docsIdx: true}, 0},
		{"all present (APP.md)", setup{reviewMD: true, appMD: true}, 0},
		{"both handbooks present", setup{reviewMD: true, docsIdx: true, appMD: true}, 0},
		{"missing REVIEW.md", setup{docsIdx: true}, 1},
		{"missing handbook", setup{reviewMD: true}, 1},
		{"missing both", setup{}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.setup.reviewMD {
				writeFile(t, filepath.Join(root, "REVIEW.md"), "ok")
			}
			if tc.setup.docsIdx {
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "docs", "index.md"), validHandbook)
			}
			if tc.setup.appMD {
				writeFile(t, filepath.Join(root, "APP.md"), validHandbook)
			}
			got := checkReviewArtifacts(root)
			if len(got) != tc.wantLen {
				t.Fatalf("len(missing) = %d, want %d (%+v)", len(got), tc.wantLen, got)
			}
		})
	}
}

func TestCheckReviewArtifacts_Subtitle(t *testing.T) {
	longSubtitle := strings.Repeat("a", maxSubtitleBytes+1)
	cases := []struct {
		name        string
		handbook    string
		wantMissing bool
	}{
		{"valid subtitle", validHandbook, false},
		{"no frontmatter", "# Welcome\n\nNo frontmatter here.\n", true},
		{"frontmatter without subtitle", "---\ntitle: Acme\n---\n\n# Welcome\n", true},
		{"empty subtitle", "---\nsubtitle: \"\"\n---\n\n# Welcome\n", true},
		{"too long", "---\nsubtitle: " + longSubtitle + "\n---\n", true},
		{"placeholder TODO", "---\nsubtitle: TODO fill this in\n---\n", true},
		{"placeholder angle", "---\nsubtitle: <one short sentence>\n---\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "REVIEW.md"), "ok")
			writeFile(t, filepath.Join(root, "APP.md"), tc.handbook)
			got := checkReviewArtifacts(root)
			if tc.wantMissing && len(got) != 1 {
				t.Fatalf("expected 1 missing artifact (subtitle), got %d (%+v)", len(got), got)
			}
			if !tc.wantMissing && len(got) != 0 {
				t.Fatalf("expected gate to pass, got %d missing (%+v)", len(got), got)
			}
		})
	}
}

func TestCheckReviewArtifacts_HandbookDirIsNotFile(t *testing.T) {
	// A directory named APP.md should not be accepted as a handbook file.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "REVIEW.md"), "ok")
	if err := os.MkdirAll(filepath.Join(root, "APP.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := checkReviewArtifacts(root)
	if len(got) != 1 {
		t.Fatalf("expected 1 missing artifact (handbook), got %d (%+v)", len(got), got)
	}
}

func TestWriteReviewGateError_MentionsArtifactsAndSkipFlag(t *testing.T) {
	var buf bytes.Buffer
	writeReviewGateError(&buf, []missingArtifact{
		{what: "REVIEW.md", hint: "see guardrails.md"},
		{what: "handbook", hint: "see user-docs.md"},
	})
	out := buf.String()
	for _, want := range []string{"REVIEW.md", "handbook", "--skip-review", "guardrails.md", "user-docs.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("error output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
