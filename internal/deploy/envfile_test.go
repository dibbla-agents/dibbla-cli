package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTempEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp env: %v", err)
	}
	return p
}

func TestMergeEnvFileAndFlags_FlagsOnly(t *testing.T) {
	got, err := MergeEnvFileAndFlags("", []string{"A=1", "B=2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"A": "1", "B": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeEnvFileAndFlags_FileOnly(t *testing.T) {
	p := writeTempEnv(t, "# comment\nexport A=1\nB=two\n")
	got, err := MergeEnvFileAndFlags(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"A": "1", "B": "two"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeEnvFileAndFlags_FlagsOverrideFile(t *testing.T) {
	p := writeTempEnv(t, "A=fromfile\nB=fromfile\n")
	got, err := MergeEnvFileAndFlags(p, []string{"B=flag", "C=flag"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"A": "fromfile", "B": "flag", "C": "flag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeEnvFileAndFlags_LastFlagWins(t *testing.T) {
	got, err := MergeEnvFileAndFlags("", []string{"A=1", "A=2", "A=3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["A"] != "3" {
		t.Errorf("got A=%q, want 3", got["A"])
	}
}

func TestMergeEnvFileAndFlags_ValuesWithEquals(t *testing.T) {
	got, err := MergeEnvFileAndFlags("", []string{"DSN=postgres://u:p@h/db?sslmode=require"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["DSN"] != "postgres://u:p@h/db?sslmode=require" {
		t.Errorf("value with = not preserved: %q", got["DSN"])
	}
}

func TestMergeEnvFileAndFlags_MissingFile(t *testing.T) {
	_, err := MergeEnvFileAndFlags("/no/such/file.env", nil)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMergeEnvFileAndFlags_InvalidFlag(t *testing.T) {
	for _, bad := range []string{"NOEQUALS", "=novalue"} {
		if _, err := MergeEnvFileAndFlags("", []string{bad}); err == nil {
			t.Errorf("expected error for invalid flag %q", bad)
		}
	}
}

func TestMergeEnvFileAndFlags_EmptyIsNonNil(t *testing.T) {
	got, err := MergeEnvFileAndFlags("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}
