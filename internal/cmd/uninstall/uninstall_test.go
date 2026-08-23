package uninstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	updatecmd "github.com/dibbla-agents/dibbla-cli/internal/cmd/update"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/credential/credtest"
	"github.com/dibbla-agents/dibbla-cli/internal/skillregistry"
)

// resetFlags returns flag state to defaults between tests.
func resetFlags() {
	flagYes = false
	flagDryRun = false
	flagKeepConfig = false
	flagKeepSkills = false
	flagSkillOnly = false
}

// isolateRegistry points the skill-installs registry at a temp file, and the
// CLI config directory and credential store at fresh temporaries, so tests
// neither read nor mutate the developer's real state.
//
// The config-dir half became necessary when uninstall started enumerating
// named contexts: buildPlan now READS ~/.config/dibbla to name what it will
// remove, so an unisolated test's plan depends on whoever is running it. It
// failed on the first machine it met for exactly that reason.
func isolateRegistry(t *testing.T) *credtest.Fake {
	t.Helper()
	dir := t.TempDir()
	cleanup := skillregistry.SetPathForTest(filepath.Join(dir, "skill-installs.json"))
	t.Cleanup(cleanup)
	fake, _ := credtest.Install(t)
	return fake
}

func labels(steps []step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.label
	}
	return out
}

func mustContain(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, s := range haystack {
		if s == needle {
			return
		}
	}
	t.Errorf("expected %q in %v", needle, haystack)
}

func TestBuildPlan_ScriptInstall_FullPlan(t *testing.T) {
	resetFlags()
	isolateRegistry(t)
	_ = skillregistry.Record("dibbla", "/proj/a")
	_ = skillregistry.Record("dibbla", "/proj/b")

	plan := buildPlan(updatecmd.MethodScript, "/usr/local/bin/dibbla")
	got := labels(plan)

	// Skill steps must come before config steps; binary step must be last.
	mustContain(t, got, "skill files at /proj/a (id=dibbla)")
	mustContain(t, got, "skill files at /proj/b (id=dibbla)")
	mustContain(t, got, "stored credentials (OS keychain + user-level file)")
	mustContain(t, got, "binary at /usr/local/bin/dibbla")

	if got[len(got)-1] != "binary at /usr/local/bin/dibbla" {
		t.Errorf("binary step should be last; got order: %v", got)
	}
}

func TestBuildPlan_HomebrewInstall_BinarySkipped(t *testing.T) {
	resetFlags()
	isolateRegistry(t)

	plan := buildPlan(updatecmd.MethodHomebrew, "/opt/homebrew/bin/dibbla")
	var binStep *step
	for i := range plan {
		if strings.HasPrefix(plan[i].label, "binary at ") {
			binStep = &plan[i]
		}
	}
	if binStep == nil {
		t.Fatal("expected a binary step")
	}
	if !binStep.skipped {
		t.Error("binary step should be skipped for homebrew")
	}
	if !strings.Contains(binStep.reason, "brew uninstall dibbla") {
		t.Errorf("expected brew uninstall hint in reason, got %q", binStep.reason)
	}
}

func TestBuildPlan_KeepConfig_OnlySkillsAndBinary(t *testing.T) {
	resetFlags()
	flagKeepConfig = true
	isolateRegistry(t)
	_ = skillregistry.Record("dibbla", "/proj/a")

	plan := buildPlan(updatecmd.MethodScript, "/bin/dibbla")
	for _, l := range labels(plan) {
		if strings.Contains(l, "keychain") || strings.Contains(l, "templates cache") {
			t.Errorf("--keep-config should suppress %q", l)
		}
	}
}

func TestBuildPlan_KeepSkills_NoSkillSteps(t *testing.T) {
	resetFlags()
	flagKeepSkills = true
	isolateRegistry(t)
	_ = skillregistry.Record("dibbla", "/proj/a")

	plan := buildPlan(updatecmd.MethodScript, "/bin/dibbla")
	for _, l := range labels(plan) {
		if strings.Contains(l, "skill files at") {
			t.Errorf("--keep-skills should suppress %q", l)
		}
	}
}

func TestBuildPlan_SkillOnly_NoBinaryNoConfig(t *testing.T) {
	resetFlags()
	flagSkillOnly = true
	isolateRegistry(t)
	_ = skillregistry.Record("dibbla", "/proj/a")

	plan := buildPlan(updatecmd.MethodScript, "/bin/dibbla")
	for _, l := range labels(plan) {
		if strings.HasPrefix(l, "binary at ") {
			t.Errorf("--skill-only should not include binary step: %q", l)
		}
		if strings.Contains(l, "keychain") {
			t.Errorf("--skill-only should not include config step: %q", l)
		}
	}
	if len(plan) == 0 {
		t.Error("expected at least one skill step")
	}
}

func TestBuildPlan_GoInstall_BinarySkippedWithGoCleanHint(t *testing.T) {
	resetFlags()
	isolateRegistry(t)

	plan := buildPlan(updatecmd.MethodGoInstall, "/home/user/go/bin/dibbla")
	var binStep *step
	for i := range plan {
		if strings.HasPrefix(plan[i].label, "binary at ") {
			binStep = &plan[i]
		}
	}
	if binStep == nil || !binStep.skipped {
		t.Fatal("binary step missing or not skipped")
	}
	if !strings.Contains(binStep.reason, "go clean -i") {
		t.Errorf("expected go clean hint, got %q", binStep.reason)
	}
}

func TestPrintPlan_RendersAllLabels(t *testing.T) {
	resetFlags()
	isolateRegistry(t)
	_ = skillregistry.Record("dibbla", "/proj/x")

	plan := buildPlan(updatecmd.MethodScript, "/bin/dibbla")
	var buf bytes.Buffer
	printPlan(&buf, plan, updatecmd.MethodScript, "/bin/dibbla")
	out := buf.String()
	if !strings.Contains(out, "/proj/x") {
		t.Errorf("plan output missing skill path:\n%s", out)
	}
	if !strings.Contains(out, "/bin/dibbla") {
		t.Errorf("plan output missing binary path:\n%s", out)
	}
}

func TestPrintPlan_EmptyPlan_PrintsNothingToDo(t *testing.T) {
	var buf bytes.Buffer
	printPlan(&buf, nil, updatecmd.MethodUnknown, "")
	if !strings.Contains(buf.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do' message, got %q", buf.String())
	}
}

func TestRemoveDirIfEmpty(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	nonEmpty := filepath.Join(dir, "nonempty")
	for _, p := range []string{empty, nonEmpty} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeDirIfEmpty(empty); err != nil {
		t.Errorf("empty: unexpected err %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Error("empty dir should be removed")
	}

	if err := removeDirIfEmpty(nonEmpty); err != nil {
		t.Errorf("nonEmpty: unexpected err %v", err)
	}
	if _, err := os.Stat(nonEmpty); err != nil {
		t.Errorf("nonempty dir should remain: %v", err)
	}

	if err := removeDirIfEmpty(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("missing dir should be no-op, got %v", err)
	}
}

func TestRemoveIfExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeIfExists(path); err != nil {
		t.Errorf("unexpected err %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
	if err := removeIfExists(path); err != nil {
		t.Errorf("missing file should be no-op, got %v", err)
	}
}

func TestExecutePlan_StopsOnBinaryFailure(t *testing.T) {
	resetFlags()
	called := []string{}
	steps := []step{
		{label: "config", exec: func() error { called = append(called, "config"); return nil }},
		{label: "binary at /bad", exec: func() error {
			called = append(called, "binary")
			return os.ErrPermission
		}},
		{label: "after-binary", exec: func() error { called = append(called, "after"); return nil }},
	}
	var buf bytes.Buffer
	err := executePlan(&buf, steps, updatecmd.MethodScript, "/bad")
	if err == nil {
		t.Fatal("expected error from binary step")
	}
	if len(called) != 2 || called[0] != "config" || called[1] != "binary" {
		t.Errorf("unexpected call order: %v", called)
	}
}

func TestExecutePlan_ContinuesOnNonBinaryFailure(t *testing.T) {
	resetFlags()
	called := []string{}
	steps := []step{
		{label: "config", exec: func() error { called = append(called, "config"); return os.ErrPermission }},
		{label: "skill files at /a (id=dibbla)", exec: func() error { called = append(called, "skill"); return nil }},
	}
	var buf bytes.Buffer
	err := executePlan(&buf, steps, updatecmd.MethodHomebrew, "")
	if err != nil {
		t.Errorf("non-binary failure should not be fatal, got %v", err)
	}
	if len(called) != 2 {
		t.Errorf("expected both steps invoked, got %v", called)
	}
}

func TestBuildPlan_NamesEveryContextItWillRemove(t *testing.T) {
	// --dry-run is the user's chance to check what uninstall is about to do, so
	// the plan has to name the contexts rather than print a generic line they
	// cannot verify against.
	resetFlags()
	isolateRegistry(t)
	cfg := &contextcfg.Config{Current: "prod", Contexts: map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com"},
		"haja": {APIURL: "https://api.haja.fatshark.se"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got := labels(buildPlan(updatecmd.MethodScript, "/usr/local/bin/dibbla"))
	var credLabel string
	for _, l := range got {
		if strings.Contains(l, "stored credentials") {
			credLabel = l
		}
	}
	if !strings.Contains(credLabel, "haja") || !strings.Contains(credLabel, "prod") {
		t.Errorf("the plan does not name the contexts it will remove: %q", credLabel)
	}
}

func TestUninstall_RemovesEveryContextTokenAndTheContextList(t *testing.T) {
	resetFlags()
	fake := isolateRegistry(t)
	cfg := &contextcfg.Config{Current: "prod", Contexts: map[string]contextcfg.Context{
		"prod": {APIURL: "https://api.dibbla.com"},
		"haja": {APIURL: "https://api.haja.fatshark.se"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fake.Items[credtest.ContextToken("prod")] = "tok-prod"
	fake.Items[credtest.ContextToken("haja")] = "tok-haja"
	fake.Items["api_token"] = "legacy"
	if err := credential.SetContextTokenFile("haja", "tok-haja", ""); err != nil {
		t.Fatal(err)
	}
	// A credentials file with no config.yaml entry: a hand-edited file, or a
	// crash between the two writes. Enumerating only the config would leave
	// this bearer token on disk after uninstall reported success.
	if err := credential.SetContextTokenFile("orphan", "tok-orphan", ""); err != nil {
		t.Fatal(err)
	}

	var found *step
	for i := range buildPlan(updatecmd.MethodScript, "/usr/local/bin/dibbla") {
		p := buildPlan(updatecmd.MethodScript, "/usr/local/bin/dibbla")[i]
		if strings.Contains(p.label, "stored credentials") {
			found = &p
			break
		}
	}
	if found == nil {
		t.Fatal("no credentials step in the plan")
	}
	if err := found.exec(); err != nil {
		t.Fatalf("credentials step: %v", err)
	}

	for _, name := range []string{"prod", "haja"} {
		if tok, _ := credential.GetContextToken(name); tok != "" {
			t.Errorf("context %s still has a stored token: %q", name, tok)
		}
	}
	if tok, _ := credential.GetToken(); tok != "" {
		t.Errorf("the legacy token survived uninstall: %q", tok)
	}
	if contextcfg.Exists() {
		t.Error("config.yaml survived uninstall")
	}
	if left := credential.ListContextTokenFiles(); len(left) != 0 {
		t.Errorf("credentials files left behind: %v — including any with no config.yaml entry", left)
	}
}
