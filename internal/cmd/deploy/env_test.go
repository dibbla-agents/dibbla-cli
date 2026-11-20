package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

// `dibbla env pull` (DIB-919): values live in Dibbla, names in the code, and
// the file it writes must never travel back. These tests pin the file
// contract (header, merge-in-place, --replace), the .gitignore line, the two
// output modes, the linked-folder default and the viewer refusal — and that
// no value ever reaches stdout/stderr outside --stdout/--json. Since DIB-1337
// secrets arrive by name only, and a secret line the developer filled in is
// theirs: never overwritten, not even by --replace.

const envDoc = `{"deployment_alias":"shop","variables":[
  {"name":"DIBBLA_ALIAS","value":"shop","source":"platform"},
  {"name":"LOG_LEVEL","value":"info","source":"inline"},
  {"name":"MOTD","value":"costs $5 today","source":"inline"}
],"secrets":[
  {"name":"API_KEY","source":"deployment"},
  {"name":"DATABASE_URL_SHOP","source":"deployment"}
]}`

// envSecretsBlock is what a pull appends for envDoc's two secrets when the
// file names neither.
const envSecretsBlock = "\n" + secretsBlockHeader + "\n" +
	"API_KEY=\n" +
	"# DATABASE_URL_SHOP: for a connection of your own, use \"$(dibbla db connect shop -q)\" in the start command — it carries your API token, so not in this file\n" +
	"DATABASE_URL_SHOP=\n"

func newEnvServer(t *testing.T, status int, body string) (*httptest.Server, *recordedRequest) {
	t.Helper()
	last := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*last = recordedRequest{Method: r.Method, Path: r.URL.Path + "?" + r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, last
}

func pull(t *testing.T, srv *httptest.Server, in envPullInput) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.APIURL, in.APIToken = srv.URL, "tok"
	code = runEnvPullCore(&out, &errb, in)
	return code, out.String(), errb.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestEnvPullWritesEnvLocalWithHeaderAndGitignoreLine(t *testing.T) {
	srv, last := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()
	code, stdout, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if last.Path != "/api/deploy/deployments/shop/env?" || last.Auth != "Bearer tok" {
		t.Errorf("request: %+v", *last)
	}

	got := readFile(t, filepath.Join(dir, ".env.local"))
	want := envHeader + "\n" +
		"DIBBLA_ALIAS=shop\n" +
		"LOG_LEVEL=info\n" +
		"MOTD='costs $5 today'\n" +
		envSecretsBlock
	if got != want {
		t.Errorf(".env.local:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env.local")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); gi != ".env.local\n" {
		t.Errorf(".gitignore = %q", gi)
	}
	for _, want := range []string{"3 variable(s)", "app shop", "2 inline, 1 platform", "2 secret(s) by name only", "0 already set", "added .env.local to .gitignore"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "kept your local values") {
		t.Errorf("no secret had a local value, but stdout says one was kept:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, "$5") {
		t.Errorf("a value leaked into the terminal")
	}
}

func TestEnvPullUpdatesInPlaceAndKeepsLocalLines(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()
	// API_KEY has a value from a pull before DIB-1337: the app's real secret.
	existing := "# my notes\nLOG_LEVEL=debug\nPORT=3001\n\nAPI_KEY=sk-live-old-pulled\n"
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte(existing), 0o600)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\n.env.local\n"), 0o644)

	code, stdout, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	got := readFile(t, filepath.Join(dir, ".env.local"))
	want := envHeader + "\n" +
		"# my notes\n" +
		"LOG_LEVEL=info\n" + // refreshed where it stood
		"PORT=3001\n" + // a local extra, kept
		"\n" +
		"API_KEY=sk-live-old-pulled\n" + // a secret's local line: never touched
		"DIBBLA_ALIAS=shop\n" +
		"MOTD='costs $5 today'\n" +
		"\n" + secretsBlockHeader + "\n" +
		"# DATABASE_URL_SHOP: for a connection of your own, use \"$(dibbla db connect shop -q)\" in the start command — it carries your API token, so not in this file\n" +
		"DATABASE_URL_SHOP=\n"
	if got != want {
		t.Errorf(".env.local:\n%s\nwant:\n%s", got, want)
	}
	for _, want := range []string{"kept 2 local line(s)", "1 already set", "kept your local values for API_KEY", "real secrets"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout+stderr, "sk-live-old-pulled") {
		t.Errorf("a local secret value reached the terminal")
	}
	if strings.Contains(stdout, "added .env.local") {
		t.Errorf(".gitignore already had the line; stdout says it was added:\n%s", stdout)
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); gi != "node_modules\n.env.local\n" {
		t.Errorf(".gitignore changed: %q", gi)
	}

	// A second pull is a no-op on the file: one header, one secrets block.
	pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if again := readFile(t, filepath.Join(dir, ".env.local")); again != want {
		t.Errorf("second pull changed the file:\n%s", again)
	}
}

func TestEnvPullReplaceRewritesTheWholeFileButKeepsFilledSecrets(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("PORT=3001\nAPI_KEY=sk-test-mine\nLOG_LEVEL=debug\n"), 0o600)
	code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", Replace: true})
	if code != 0 {
		t.Fatal(stderr)
	}
	got := readFile(t, filepath.Join(dir, ".env.local"))
	want := envHeader + "\n" +
		"DIBBLA_ALIAS=shop\n" +
		"LOG_LEVEL=info\n" +
		"MOTD='costs $5 today'\n" +
		"\n" + secretsBlockHeader + "\n" +
		"API_KEY=sk-test-mine\n" +
		"# DATABASE_URL_SHOP: for a connection of your own, use \"$(dibbla db connect shop -q)\" in the start command — it carries your API token, so not in this file\n" +
		"DATABASE_URL_SHOP=\n"
	if got != want {
		t.Errorf("--replace:\n%s\nwant:\n%s", got, want)
	}
}

// TestEnvPullNeverWritesAValueUnderASecretsName: if a server ever sends a
// name both as a variable and as a secret, the secret wins and no value is
// written for it.
func TestEnvPullNeverWritesAValueUnderASecretsName(t *testing.T) {
	srv, _ := newEnvServer(t, 200, `{"deployment_alias":"shop",
	  "variables":[{"name":"API_KEY","value":"sk-live-leaked","source":"inline"}],
	  "secrets":[{"name":"API_KEY","source":"deployment"}]}`)
	dir := t.TempDir()
	if code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"}); code != 0 {
		t.Fatal(stderr)
	}
	if got := readFile(t, filepath.Join(dir, ".env.local")); strings.Contains(got, "sk-live-leaked") || !strings.Contains(got, "\nAPI_KEY=\n") {
		t.Errorf(".env.local:\n%s", got)
	}
}

func TestEnvPullStdoutAndJSONWriteNoFile(t *testing.T) {
	srv, last := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()

	code, stdout, _ := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", Service: "worker", Stdout: true})
	if code != 0 {
		t.Fatal(code)
	}
	if last.Path != "/api/deploy/deployments/shop/env?service=worker" {
		t.Errorf("--service not sent: %s", last.Path)
	}
	// A server from before the read view (DIB-1837): each name is effective
	// and live, scopes named as the read view names them.
	if stdout != "# API_KEY is a secret (app, live): Dibbla never hands out its value\n"+
		"# DATABASE_URL_SHOP is a secret (app, live): Dibbla never hands out its value\n"+
		"# DIBBLA_ALIAS: variable, platform, origin platform, live\nDIBBLA_ALIAS=shop\n"+
		"# LOG_LEVEL: variable, app, origin deploy, live\nLOG_LEVEL=info\n"+
		"# MOTD: variable, app, origin deploy, live\nMOTD='costs $5 today'\n" {
		t.Errorf("--stdout:\n%s", stdout)
	}

	code, stdout, _ = pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", JSON: true})
	var doc apps.EnvResponse
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || len(doc.Variables) != 3 || doc.Variables[0].Source != "platform" || len(doc.Secrets) != 2 {
		t.Errorf("--json: exit %d, %s", code, stdout)
	}

	for _, f := range []string{".env.local", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s was written in an output-only mode", f)
		}
	}
	if code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", JSON: true, Stdout: true}); code != 5 || !strings.Contains(stderr, "pick one") {
		t.Errorf("--stdout --json: exit %d %s", code, stderr)
	}
}

func TestEnvPullDefaultsToTheLinkedApp(t *testing.T) {
	gitEnv(t)
	work, _ := linkedRepo(t)
	srv, last := newEnvServer(t, 200, envDoc)
	if code, _, stderr := pull(t, srv, envPullInput{Dir: work}); code != 0 {
		t.Fatal(stderr)
	}
	if !strings.HasPrefix(last.Path, "/api/deploy/deployments/shop/env") {
		t.Errorf("linked app not used: %s", last.Path)
	}
	// .gitignore in the repository root, so git never sees the file.
	if gi := readFile(t, filepath.Join(work, ".gitignore")); !strings.Contains(gi, ".env.local\n") {
		t.Errorf(".gitignore = %q", gi)
	}
	if out := git(t, work, "status", "--porcelain", "--ignored", ".env.local"); !strings.HasPrefix(out, "!!") {
		t.Errorf("git does not ignore .env.local: %q", out)
	}

	plain := t.TempDir()
	code, _, stderr := pull(t, srv, envPullInput{Dir: plain})
	if code != 5 || !strings.Contains(stderr, "not linked") || !strings.Contains(stderr, "--deployment") {
		t.Errorf("unlinked folder: exit %d %s", code, stderr)
	}
}

func TestEnvPullViewerIsRefused(t *testing.T) {
	srv, _ := newEnvServer(t, 403, `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"role viewer may not deploy; owner, admin or developer can"}}`)
	dir := t.TempDir()
	code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if code == 0 {
		t.Fatal("viewer was served")
	}
	if !strings.Contains(stderr, "refused") || !strings.Contains(stderr, "deploy roles") {
		t.Errorf("stderr: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.local")); err == nil {
		t.Error(".env.local written on refusal")
	}
}

func TestEnvPullHelpStatesTheModel(t *testing.T) {
	for _, want := range []string{"Values live in Dibbla, names live in the code", ".env.example", "write-only", "never overwritten", "dibbla db connect"} {
		if !strings.Contains(envPullCmd.Long, want) {
			t.Errorf("--help lacks %q", want)
		}
	}
}

// DIB-1339: a plain env var the server flags as secret-looking is named, with
// what to do about it — and its value is not printed.
func TestEnvPullNamesVariablesThatLookLikeSecrets(t *testing.T) {
	srv, _ := newEnvServer(t, 200, `{"deployment_alias":"shop","variables":[
	  {"name":"PORT","value":"3000","source":"inline"},
	  {"name":"STRIPE_SECRET_KEY","value":"sk_live_SYNTHETIC","source":"inline","looks_like_secret":true}
	],"secrets":[]}`)
	code, stdout, stderr := pull(t, srv, envPullInput{Dir: t.TempDir(), Deployment: "shop"})
	if code != 0 {
		t.Fatal(stderr)
	}
	if !strings.Contains(stdout, "STRIPE_SECRET_KEY look like secrets") || strings.Contains(stdout, "PORT look") || !strings.Contains(stdout, "dibbla env promote NAME -d shop") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, "sk_live_SYNTHETIC") {
		t.Error("the value reached the terminal")
	}
}

// envViewDoc is a read-view answer (DIB-1837): an inherited global secret
// shadowed by the app's, a Console-only key, a variable saved after the app
// started.
const envViewDoc = `{"deployment_alias":"shop","live":false,"entries":[
  {"name":"AGENT_URL","kind":"secret","scope":"app","origin":"console","effective":true,"live":true},
  {"name":"API_KEY","kind":"secret","scope":"global","effective":false,"live":true},
  {"name":"API_KEY","kind":"secret","scope":"app","effective":true,"live":false},
  {"name":"LOG_LEVEL","kind":"variable","value":"info","scope":"app","origin":"deploy","effective":true,"live":true},
  {"name":"STRIPE_KEY","kind":"secret","scope":"app","effective":false,"live":true},
  {"name":"STRIPE_KEY","kind":"variable","value_hidden":true,"scope":"app","origin":"deploy","effective":true,"live":true}
],"warnings":[
  {"code":"shadowed","name":"API_KEY","message":"API_KEY is set as secret (global), secret (app); the app gets the secret (app)."},
  {"code":"variable_and_secret","name":"STRIPE_KEY","message":"STRIPE_KEY is set as secret (app), variable (app); the app gets the variable (app). Keep one: remove the variable or the secret."}
],"variables":[{"name":"LOG_LEVEL","value":"info","source":"inline"}],
"secrets":[{"name":"AGENT_URL","source":"console"},{"name":"API_KEY","source":"deployment"},{"name":"STRIPE_KEY","source":"deployment"}]}`

func TestEnvPullStdoutShowsTheReadView(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envViewDoc)
	code, stdout, _ := pull(t, srv, envPullInput{Dir: t.TempDir(), Deployment: "shop", Stdout: true})
	if code != 0 {
		t.Fatal(code)
	}
	want := "# AGENT_URL is a secret (app, origin console, live): Dibbla never hands out its value\n" +
		"# API_KEY is a secret (app, restart needed): Dibbla never hands out its value\n" +
		"# LOG_LEVEL: variable, app, origin deploy, live\nLOG_LEVEL=info\n" +
		"# STRIPE_KEY is a variable with a secret's name (app, origin deploy, live): its value is not handed out\n" +
		"# ! API_KEY is set as secret (global), secret (app); the app gets the secret (app).\n" +
		"# ! STRIPE_KEY is set as secret (app), variable (app); the app gets the variable (app). Keep one: remove the variable or the secret.\n" +
		"# Some values were saved after the app started; restart it to apply them: dibbla apps restart shop\n"
	if stdout != want {
		t.Errorf("--stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestEnvPullFileModeSaysWhatIsNotLive(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envViewDoc)
	code, stdout, _ := pull(t, srv, envPullInput{Dir: t.TempDir(), Deployment: "shop"})
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"! API_KEY is set as", "dibbla apps restart shop"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestEnvPullHelpExplainsLive(t *testing.T) {
	if strings.Contains(envPullCmd.Long, "exactly as the running app gets it") {
		t.Error("--help still claims the pull is exactly what the app gets")
	}
	for _, want := range []string{"restart needed", "live", "dibbla apps get"} {
		if !strings.Contains(envPullCmd.Long, want) {
			t.Errorf("--help lacks %q", want)
		}
	}
}
