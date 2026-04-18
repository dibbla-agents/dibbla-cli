package run

import (
	"strings"
	"testing"
)

func TestResolveTaskPath_NoArg_DefaultsToCwdYAML(t *testing.T) {
	path, isURL, cleanup, err := resolveTaskPath(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup != nil {
		t.Error("expected no cleanup for local path")
	}
	if isURL {
		t.Error("expected isURL=false for default path")
	}
	if !strings.HasSuffix(path, "dibbla-task.yaml") {
		t.Errorf("expected path to end with dibbla-task.yaml, got %q", path)
	}
}

func TestResolveTaskPath_LocalPath_AbsolutizesIt(t *testing.T) {
	path, isURL, cleanup, err := resolveTaskPath([]string{"./relative/path.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup != nil {
		t.Error("expected no cleanup for local path")
	}
	if isURL {
		t.Error("expected isURL=false for local path")
	}
	if !strings.HasPrefix(path, "/") {
		t.Errorf("expected absolute path, got %q", path)
	}
	if !strings.HasSuffix(path, "relative/path.yaml") {
		t.Errorf("expected path to end with relative/path.yaml, got %q", path)
	}
}

func TestBuildEnv_ParsesKVPairs(t *testing.T) {
	flagEnv = []string{"FOO=bar", "EMPTY=", "WITH_EQUALS=a=b=c"}
	defer func() { flagEnv = nil }()

	env, err := buildEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["FOO"] != "bar" {
		t.Errorf("FOO: got %q, want bar", env["FOO"])
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("EMPTY: got %q (ok=%v), want empty string", v, ok)
	}
	if env["WITH_EQUALS"] != "a=b=c" {
		t.Errorf("WITH_EQUALS: got %q, want a=b=c", env["WITH_EQUALS"])
	}
}

func TestBuildEnv_RejectsInvalidPair(t *testing.T) {
	flagEnv = []string{"NO_EQUALS_HERE"}
	defer func() { flagEnv = nil }()

	_, err := buildEnv()
	if err == nil {
		t.Fatal("expected error for env value without '='")
	}
}

func TestPickFormatter_Defaults(t *testing.T) {
	flagFormat = ""
	defer func() { flagFormat = "plain" }()
	f, err := pickFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil formatter for empty format")
	}
}

func TestPickFormatter_Plain(t *testing.T) {
	flagFormat = "plain"
	f, err := pickFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil formatter for 'plain'")
	}
}

func TestPickFormatter_GH(t *testing.T) {
	flagFormat = "gh"
	defer func() { flagFormat = "plain" }()
	f, err := pickFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil formatter for 'gh'")
	}
}

func TestPickFormatter_Invalid(t *testing.T) {
	flagFormat = "json"
	defer func() { flagFormat = "plain" }()
	_, err := pickFormatter()
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}
