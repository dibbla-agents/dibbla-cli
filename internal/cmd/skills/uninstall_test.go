package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripMarkerBlock_RemovesBlockOnly(t *testing.T) {
	block := renderPointerBlock("dibbla", "v1.0.0")
	existing := "# Project\n\nIntro line.\n\n" + block + "\n\nFooter line.\n"
	out, removed := stripMarkerBlock(existing)
	if !removed {
		t.Fatal("expected removed=true")
	}
	if strings.Contains(out, markerOpen) || strings.Contains(out, markerClose) {
		t.Errorf("markers still present after strip:\n%s", out)
	}
	if !strings.Contains(out, "Intro line.") {
		t.Error("intro content missing")
	}
	if !strings.Contains(out, "Footer line.") {
		t.Error("footer content missing")
	}
}

func TestStripMarkerBlock_NoMarkers_Unchanged(t *testing.T) {
	in := "# Project\n\nNo markers anywhere.\n"
	out, removed := stripMarkerBlock(in)
	if removed {
		t.Error("expected removed=false on no-marker input")
	}
	if out != in {
		t.Errorf("input should be unchanged; got %q", out)
	}
}

func TestStripMarkerBlock_HalfMarker_Unchanged(t *testing.T) {
	in := "before\n\n" + markerOpen + "\n\nleftover open marker only\n"
	out, removed := stripMarkerBlock(in)
	if removed {
		t.Error("expected removed=false when close marker missing")
	}
	if out != in {
		t.Errorf("input should be unchanged; got %q", out)
	}
}

func TestStripMarkerBlock_OnlyBlock_BecomesEmpty(t *testing.T) {
	block := renderPointerBlock("dibbla", "v1.0.0")
	out, removed := stripMarkerBlock(block + "\n")
	if !removed {
		t.Fatal("expected removed=true")
	}
	if out != "" {
		t.Errorf("expected empty result, got %q", out)
	}
}

func TestStripMarkerBlock_PreservesCRLF(t *testing.T) {
	block := renderPointerBlock("dibbla", "v1.0.0")
	// Convert to CRLF endings.
	crlfBlock := strings.ReplaceAll(block, "\n", "\r\n")
	in := "# Title\r\n\r\n" + crlfBlock + "\r\n\r\nFooter.\r\n"
	out, removed := stripMarkerBlock(in)
	if !removed {
		t.Fatal("expected removed=true")
	}
	if !strings.Contains(out, "\r\n") {
		t.Errorf("CRLF endings should be preserved; got %q", out)
	}
	stripped := strings.ReplaceAll(out, "\r\n", "")
	if strings.Contains(stripped, "\n") {
		t.Errorf("output contains bare LF: %q", out)
	}
}

func TestUninstallSkill_RemovesDirAndStripsMarkers(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "dibbla")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	block := renderPointerBlock("dibbla", "v1.0.0")
	agentsContent := "# Project\n\nMy notes.\n\n" + block + "\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, errs := UninstallSkill(root, "dibbla")
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(removed) == 0 {
		t.Error("expected at least one removed path")
	}

	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Errorf("skill dir not removed: err=%v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md missing: %v", err)
	}
	if strings.Contains(string(data), markerOpen) {
		t.Error("AGENTS.md still has marker block")
	}
	if !strings.Contains(string(data), "My notes.") {
		t.Error("AGENTS.md user content was lost")
	}
}

func TestUninstallSkill_DeletesAgentsWhenOnlyContainedBlock(t *testing.T) {
	root := t.TempDir()
	block := renderPointerBlock("dibbla", "v1.0.0")
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(block+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errs := UninstallSkill(root, "dibbla"); len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("expected AGENTS.md to be deleted; err=%v", err)
	}
}

func TestUninstallSkill_LeavesAgentsAloneWhenNoBlock(t *testing.T) {
	root := t.TempDir()
	original := "# Project\n\nNo dibbla here.\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errs := UninstallSkill(root, "dibbla"); len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("AGENTS.md modified unexpectedly:\nwant %q\ngot  %q", original, string(data))
	}
}

func TestUninstallSkill_MissingTargets_NoError(t *testing.T) {
	root := t.TempDir()
	removed, errs := UninstallSkill(root, "dibbla")
	if len(errs) > 0 {
		t.Errorf("missing targets should not error; got %v", errs)
	}
	if len(removed) != 0 {
		t.Errorf("expected nothing removed, got %v", removed)
	}
}

func TestUninstallSkill_PrunesEmptyParents(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "dibbla")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, errs := UninstallSkill(root, "dibbla"); len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}

	// .claude/skills/ should be gone (empty after dibbla removal).
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills")); !os.IsNotExist(err) {
		t.Errorf("expected empty .claude/skills/ to be pruned; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Errorf("expected empty .claude/ to be pruned; err=%v", err)
	}
}

func TestUninstallSkill_LeavesNonEmptyParents(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "dibbla")
	otherSkill := filepath.Join(root, ".claude", "skills", "other")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherSkill, "x.md"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, errs := UninstallSkill(root, "dibbla"); len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}

	// Other skill must still be present.
	if _, err := os.Stat(otherSkill); err != nil {
		t.Errorf("other skill should be untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills")); err != nil {
		t.Errorf(".claude/skills/ should not have been pruned: %v", err)
	}
}

func TestUninstallSkill_RejectsInvalidID(t *testing.T) {
	_, errs := UninstallSkill(t.TempDir(), "../escape")
	if len(errs) == 0 {
		t.Error("expected error for invalid id")
	}
}
