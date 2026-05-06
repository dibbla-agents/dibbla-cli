package manifestcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// resetFlags restores the package-level flag state between tests. Cobra binds
// these via package vars; without a reset a previous test's --json sticks.
func resetFlags() {
	validateTargetEnv = ""
	validateProfiles = nil
	validateNoPublic = false
	validateJSON = false
}

func TestValidate_HappyPath_HumanOutput(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: ./web
    port: 3000
    public: true
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
`)
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{dir})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "is valid") {
		t.Errorf("missing valid line: %q", out)
	}
	if !strings.Contains(out, "3 services") {
		t.Errorf("missing service count: %q", out)
	}
	if !strings.Contains(out, "web (public)") {
		t.Errorf("missing public marker: %q", out)
	}
	if !strings.Contains(out, "worker") || !strings.Contains(out, "redis") {
		t.Errorf("missing service names: %q", out)
	}
}

func TestValidate_HappyPath_JSON(t *testing.T) {
	resetFlags()
	validateJSON = true
	defer resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: .
    port: 3000
    public: true
`)
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{dir})
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	var rep validateReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v\noutput=%s", err, stdout.String())
	}
	if !rep.Valid {
		t.Errorf("expected valid=true: %+v", rep)
	}
	if rep.ManifestPath == "" {
		t.Errorf("expected manifest_path: %+v", rep)
	}
	if len(rep.Services) != 1 || rep.Services[0].Name != "web" || !rep.Services[0].Public {
		t.Errorf("unexpected services: %+v", rep.Services)
	}
}

func TestValidate_TargetEnvAndProfilesAreRecorded(t *testing.T) {
	resetFlags()
	validateJSON = true
	validateTargetEnv = "staging"
	validateProfiles = []string{"mailcatcher", "metrics"}
	validateNoPublic = true
	defer resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: .
    port: 3000
`)
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{dir}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep validateReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.TargetEnv != "staging" {
		t.Errorf("target_env: got %q", rep.TargetEnv)
	}
	if len(rep.Profiles) != 2 || rep.Profiles[0] != "mailcatcher" {
		t.Errorf("profiles: %v", rep.Profiles)
	}
	if !rep.NoPublic {
		t.Errorf("no_public: %v", rep.NoPublic)
	}
}

func TestValidate_AmbiguousManifest(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", "version: 1\nservices:\n  web:\n    build: .\n    port: 3000\n")
	writeManifest(t, dir, "dibbla.yml", "version: 1\nservices:\n  web:\n    build: .\n    port: 3000\n")
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{dir})
	if code != 1 {
		t.Fatalf("exit code: want 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "MANIFEST_AMBIGUOUS") {
		t.Errorf("missing MANIFEST_AMBIGUOUS in stderr: %q", stderr.String())
	}
}

func TestValidate_InvalidImageNoTag(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  cache:
    image: redis
    port: 6379
`)
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{dir})
	if code != 1 {
		t.Fatalf("exit code: want 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "MANIFEST_INVALID") {
		t.Errorf("missing error code: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "must include a tag") {
		t.Errorf("missing detail: %q", stderr.String())
	}
}

func TestValidate_ReservedServiceName(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  proxy:
    build: .
    port: 3000
`)
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "SERVICE_NAME_INVALID") {
		t.Errorf("missing SERVICE_NAME_INVALID: %q", stderr.String())
	}
}

func TestValidate_BuildAndImage(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    build: .
    image: redis:7
    port: 3000
`)
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "MANIFEST_INVALID") {
		t.Errorf("missing code: %q", stderr.String())
	}
}

func TestValidate_FailureJSON(t *testing.T) {
	resetFlags()
	validateJSON = true
	defer resetFlags()
	dir := t.TempDir()
	writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  cache:
    image: redis
    port: 6379
`)
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	var rep validateReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v\noutput=%s", err, stdout.String())
	}
	if rep.Valid {
		t.Errorf("expected valid=false")
	}
	if len(rep.Errors) == 0 || rep.Errors[0].Code != "MANIFEST_INVALID" {
		t.Errorf("expected MANIFEST_INVALID error, got %+v", rep.Errors)
	}
}

func TestValidate_NoManifest_LegacyPathInformational(t *testing.T) {
	resetFlags()
	dir := t.TempDir() // no manifest written
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{dir})
	if code != 0 {
		t.Fatalf("expected exit 0 for missing manifest, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no dibbla.yaml") {
		t.Errorf("missing informational line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "legacy single-Dockerfile path") {
		t.Errorf("missing legacy path note: %q", stdout.String())
	}
}

func TestValidate_NoManifest_JSON(t *testing.T) {
	resetFlags()
	validateJSON = true
	defer resetFlags()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{dir}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep validateReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.NoManifest || !rep.Valid {
		t.Errorf("want no_manifest+valid: %+v", rep)
	}
}

func TestValidate_DirectFilePath(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	p := writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    build: .
    port: 3000
`)
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{p})
	if code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is valid") {
		t.Errorf("missing valid line: %q", stdout.String())
	}
}

func TestValidate_NonexistentPath(t *testing.T) {
	resetFlags()
	var stdout, stderr bytes.Buffer
	code := runValidate(&stdout, &stderr, []string{"/nonexistent/dibbla-cli/path"})
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "MANIFEST_INVALID") {
		t.Errorf("expected MANIFEST_INVALID: %q", stderr.String())
	}
}

func TestValidate_DirectFilePath_WrongName(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	p := writeManifest(t, dir, "not-a-manifest.yaml", "version: 1\nservices: {}\n")
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{p}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "is not a dibbla manifest") {
		t.Errorf("missing rejection: %q", stderr.String())
	}
}

func TestSummarizeServices_PublicSortsFirst(t *testing.T) {
	resetFlags()
	dir := t.TempDir()
	p := writeManifest(t, dir, "dibbla.yaml", `
version: 1
services:
  redis:
    image: redis:7
    port: 6379
  web:
    build: .
    port: 3000
    public: true
  worker:
    build: ./w
`)
	// Use the parser directly so we exercise summarizeServices ordering.
	var stdout, stderr bytes.Buffer
	if code := runValidate(&stdout, &stderr, []string{p}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := stdout.String()
	webIdx := strings.Index(out, "web (public)")
	redisIdx := strings.Index(out, "redis")
	workerIdx := strings.Index(out, "worker")
	if webIdx < 0 || redisIdx < 0 || workerIdx < 0 {
		t.Fatalf("missing services in output: %q", out)
	}
	if !(webIdx < redisIdx && webIdx < workerIdx) {
		t.Errorf("public service should come first; got positions web=%d redis=%d worker=%d",
			webIdx, redisIdx, workerIdx)
	}
}

func TestExtractPublic(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"map default true", map[string]any{"default": true, "staging": false}, true},
		{"map default false", map[string]any{"default": false, "prod": true}, false},
		{"map without default", map[string]any{"prod": true}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := extractPublic(c.in); got != c.want {
			t.Errorf("%s: extractPublic(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
