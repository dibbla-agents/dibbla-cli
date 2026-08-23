package contextcfg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
)

const defaultURL = "https://api.dibbla.com"

func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))
	return dir
}

func TestLoad_MissingFileIsEmptyButUsable(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("a missing config file must not be an error: %v", err)
	}
	// The non-nil map is the point: callers Set() into it without a nil check.
	cfg.Set("prod", Context{APIURL: defaultURL})
	if len(cfg.Contexts) != 1 {
		t.Errorf("Set on a freshly loaded empty config did not take")
	}
}

func TestLoad_MalformedFileIsAnError(t *testing.T) {
	// Deliberately an error rather than a silent empty config: treating an
	// unreadable config as "no contexts" would drop a user who had carefully
	// pointed the CLI at a customer instance back onto production.
	dir := isolate(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte("contexts: [not, a, map]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("a malformed config.yaml must be an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error must name the file; got %v", err)
	}
}

func TestSaveLoad_RoundTripAndMode(t *testing.T) {
	isolate(t)
	in := &Config{
		Current: "haja",
		Contexts: map[string]Context{
			"prod": {APIURL: defaultURL},
			"haja": {APIURL: "https://api.haja.fatshark.se", Org: "org-1", OrgName: "Haja"},
		},
	}
	if err := in.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Current != "haja" || len(out.Contexts) != 2 {
		t.Fatalf("round trip lost data: %+v", out)
	}
	if got := out.Contexts["haja"]; got.Org != "org-1" || got.OrgName != "Haja" {
		t.Errorf("org pin lost in round trip: %+v", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(Path())
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("config.yaml mode = %v, want 0600", fi.Mode().Perm())
		}
		di, err := os.Stat(filepath.Dir(Path()))
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm() != 0o700 {
			t.Errorf("config dir mode = %v, want 0700", di.Mode().Perm())
		}
	}
}

func TestSave_DoesNotWriteSecrets(t *testing.T) {
	// The file is hand-editable and world-visible in a home directory backup;
	// the whole reason the list lives in a file and the tokens do not.
	isolate(t)
	cfg := &Config{Current: "prod", Contexts: map[string]Context{"prod": {APIURL: defaultURL, Org: "org-1"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "Token", "ak_", "secret"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("config.yaml contains %q; tokens must never be written here:\n%s", forbidden, body)
		}
	}
}

func TestDeriveName(t *testing.T) {
	cases := []struct{ in, want string }{
		{defaultURL, "prod"},
		{defaultURL + "/", "prod"},
		{"", "prod"},
		{"https://api.dibbla.net", "dibbla"},
		{"https://api.haja.fatshark.se", "haja"},
		{"https://haja.fatshark.se", "haja"},
		{"https://localhost:8080", "localhost"},
		// A bare internal host called "api" keeps its name: the "api." strip
		// exists so instances do not all collide on "api", not to forbid a
		// host actually named that.
		{"https://api.", "api"},
		{"https://api", "api"},
		// Nothing usable survives — must not derive a name that cannot be a
		// filename or a keyring key.
		{"https://", "server"},
		{"not a url at all", "server"},
	}
	for _, c := range cases {
		if got := DeriveName(c.in, defaultURL); got != c.want {
			t.Errorf("DeriveName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUniqueName_IsDeterministic(t *testing.T) {
	taken := map[string]Context{"haja": {}, "haja-2": {}}
	if got := UniqueName("haja", taken); got != "haja-3" {
		t.Errorf("UniqueName = %q, want haja-3", got)
	}
	if got := UniqueName("dev", taken); got != "dev" {
		t.Errorf("UniqueName on a free name = %q, want dev", got)
	}
	// Determinism is what makes migration idempotent: the same inputs must
	// derive the same name on a re-run after a crash.
	if UniqueName("haja", taken) != UniqueName("haja", taken) {
		t.Error("UniqueName is not deterministic")
	}
}

func TestValidName_RejectsPathTraversal(t *testing.T) {
	// config.yaml is hand-editable, so a context name is user input, and it
	// becomes part of a filename (credentials.<name>.env) holding a bearer
	// token at mode 0600. A separator here would write that file outside the
	// config directory.
	for _, bad := range []string{
		"", ".", "..", "../../evil", "a/b", `a\b`, "with space", "wi:th", "ø",
		strings.Repeat("x", 65),
	} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"prod", "dev", "haja", "a-b_c.d", "x2", strings.Repeat("x", 64)} {
		if !ValidName(good) {
			t.Errorf("ValidName(%q) = false, want true", good)
		}
	}
}

func TestFindByURL_NormalizesAndIsDeterministic(t *testing.T) {
	cfg := &Config{Contexts: map[string]Context{
		"a": {APIURL: "https://api.example.com"},
		"b": {APIURL: "https://api.other.com/"},
	}}
	if got := cfg.FindByURL("https://api.example.com/"); got != "a" {
		t.Errorf("FindByURL with a trailing slash = %q, want a", got)
	}
	if got := cfg.FindByURL("https://api.other.com"); got != "b" {
		t.Errorf("FindByURL against a stored trailing slash = %q, want b", got)
	}
	if got := cfg.FindByURL("https://api.nowhere.com"); got != "" {
		t.Errorf("FindByURL on an unknown URL = %q, want empty", got)
	}
	// The empty-URL guard only means something when a context could match it.
	// config.yaml is hand-editable, so a context with a blank api_url is a
	// state that can exist on disk, and without the guard login would "find"
	// it and refresh the wrong entry.
	cfg.Contexts["blank"] = Context{APIURL: ""}
	if got := cfg.FindByURL(""); got != "" {
		t.Errorf("FindByURL(\"\") = %q, want empty — an empty URL must not match a context with a blank api_url", got)
	}
	if got := cfg.FindByURL("   "); got != "" {
		t.Errorf("FindByURL on whitespace = %q, want empty", got)
	}
}

func TestDelete_ClearsCurrentWhenItWasTheOne(t *testing.T) {
	cfg := &Config{Current: "prod", Contexts: map[string]Context{"prod": {}, "dev": {}}}
	cfg.Delete("dev")
	if cfg.Current != "prod" {
		t.Errorf("deleting another context changed current to %q", cfg.Current)
	}
	cfg.Delete("prod")
	if cfg.Current != "" {
		t.Errorf("current = %q after deleting it, want empty — a dangling current: is an inconsistent file", cfg.Current)
	}
}

func TestSave_LeavesNoTempFilesBehind(t *testing.T) {
	dir := isolate(t)
	cfg := &Config{Current: "prod", Contexts: map[string]Context{"prod": {APIURL: defaultURL}}}
	for i := 0; i < 5; i++ {
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %q left behind in the config directory", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("config dir holds %d entries after five saves, want 1", len(entries))
	}
}

func TestSave_AllOrNothing_AFailedWriteDoesNotDamageTheExistingFile(t *testing.T) {
	// The property is that Save either fully succeeds or changes nothing, so a
	// reader never meets a truncated config.yaml. Interrupting a real write
	// mid-flight is not something a unit test can do; making the write
	// impossible is. A read-only directory stops the tempfile from being
	// created at all — while a plain in-place os.WriteFile would sail through,
	// because write permission for an existing file lives on the file, not on
	// its directory. That difference is what this test detects.
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits, so the failure cannot be induced")
	}
	dir := isolate(t)

	v1 := &Config{Current: "prod", Contexts: map[string]Context{"prod": {APIURL: defaultURL}}}
	if err := v1.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	v2 := &Config{Current: "haja", Contexts: map[string]Context{"haja": {APIURL: "https://api.haja.fatshark.se"}}}
	if err := v2.Save(); err == nil {
		t.Fatal("Save must fail when it cannot write; a silent success means it wrote in place")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("the previous config must still parse after a failed save: %v", err)
	}
	if got.Current != "prod" || len(got.Contexts) != 1 {
		t.Errorf("a failed Save damaged the existing file: %+v", got)
	}
}
