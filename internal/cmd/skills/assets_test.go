package skills

import (
	"io/fs"
	"testing"
)

func TestDibblaSkillFS_NotEmpty(t *testing.T) {
	sub, err := fs.Sub(dibblaSkillFS, dibblaSkillRoot)
	if err != nil {
		t.Fatalf("fs.Sub(%q): %v — did you run `go generate ./internal/cmd/skills/...`?", dibblaSkillRoot, err)
	}

	expected := []string{"SKILL.md", "examples.md", "guardrails.md", "reference.md"}
	for _, name := range expected {
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			t.Errorf("%s missing from embedded skill: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty in the embedded FS", name)
		}
	}
}

func TestFindSkill_Known(t *testing.T) {
	entry, err := find("dibbla")
	if err != nil {
		t.Fatalf("find(\"dibbla\"): %v", err)
	}
	if entry.id != "dibbla" {
		t.Errorf("entry.id = %q, want %q", entry.id, "dibbla")
	}
}

func TestFindSkill_Unknown(t *testing.T) {
	_, err := find("nope")
	if err == nil {
		t.Fatal("find(\"nope\"): expected error, got nil")
	}
}
