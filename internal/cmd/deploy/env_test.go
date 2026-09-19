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
// no value ever reaches stdout/stderr outside --stdout/--json.

const envDoc = `{"deployment_alias":"shop","variables":[
  {"name":"API_KEY","value":"sk-live-hunter2","source":"deployment"},
  {"name":"DATABASE_URL_SHOP","value":"postgresql://shop:p%40ss@db.example:30432/shop?sslmode=require","source":"deployment"},
  {"name":"DIBBLA_ALIAS","value":"shop","source":"platform"},
  {"name":"LOG_LEVEL","value":"info","source":"global"},
  {"name":"MOTD","value":"costs $5 today","source":"global"}
]}`

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
		"API_KEY=sk-live-hunter2\n" +
		"DATABASE_URL_SHOP=postgresql://shop:p%40ss@db.example:30432/shop?sslmode=require\n" +
		"DIBBLA_ALIAS=shop\n" +
		"LOG_LEVEL=info\n" +
		"MOTD='costs $5 today'\n"
	if got != want {
		t.Errorf(".env.local:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env.local")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); gi != ".env.local\n" {
		t.Errorf(".gitignore = %q", gi)
	}
	for _, want := range []string{"5 variable(s)", "app shop", "2 global, 2 app, 1 platform", "added .env.local to .gitignore", "real database"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, leak := range []string{"hunter2", "p%40ss", "$5"} {
		if strings.Contains(stdout+stderr, leak) {
			t.Errorf("a value leaked into the terminal: %q", leak)
		}
	}
}

func TestEnvPullUpdatesInPlaceAndKeepsLocalLines(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()
	existing := "# my notes\nLOG_LEVEL=debug\nPORT=3001\n\nAPI_KEY=old\n"
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
		"API_KEY=sk-live-hunter2\n" +
		"DATABASE_URL_SHOP=postgresql://shop:p%40ss@db.example:30432/shop?sslmode=require\n" +
		"DIBBLA_ALIAS=shop\n" +
		"MOTD='costs $5 today'\n"
	if got != want {
		t.Errorf(".env.local:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(stdout, "kept 2 local line(s)") {
		t.Errorf("stdout should count the comment and PORT as kept:\n%s", stdout)
	}
	if strings.Contains(stdout, "added .env.local") {
		t.Errorf(".gitignore already had the line; stdout says it was added:\n%s", stdout)
	}
	if gi := readFile(t, filepath.Join(dir, ".gitignore")); gi != "node_modules\n.env.local\n" {
		t.Errorf(".gitignore changed: %q", gi)
	}

	// A second pull is a no-op on the file: one header, same lines.
	pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if again := readFile(t, filepath.Join(dir, ".env.local")); again != want {
		t.Errorf("second pull changed the file:\n%s", again)
	}
}

func TestEnvPullReplaceRewritesTheWholeFile(t *testing.T) {
	srv, _ := newEnvServer(t, 200, envDoc)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("PORT=3001\nAPI_KEY=old\n"), 0o600)
	code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", Replace: true})
	if code != 0 {
		t.Fatal(stderr)
	}
	got := readFile(t, filepath.Join(dir, ".env.local"))
	if strings.Contains(got, "PORT=") || !strings.HasPrefix(got, envHeader+"\n") || !strings.Contains(got, "API_KEY=sk-live-hunter2\n") {
		t.Errorf("--replace did not rewrite the file:\n%s", got)
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
	if stdout != "API_KEY=sk-live-hunter2\nDATABASE_URL_SHOP=postgresql://shop:p%40ss@db.example:30432/shop?sslmode=require\nDIBBLA_ALIAS=shop\nLOG_LEVEL=info\nMOTD='costs $5 today'\n" {
		t.Errorf("--stdout:\n%s", stdout)
	}

	code, stdout, _ = pull(t, srv, envPullInput{Dir: dir, Deployment: "shop", JSON: true})
	var doc apps.EnvResponse
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || len(doc.Variables) != 5 || doc.Variables[2].Source != "platform" {
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

func TestEnvPullViewerIsRefusedLikeSecretsGet(t *testing.T) {
	srv, _ := newEnvServer(t, 403, `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"role viewer may not deploy; owner, admin or developer can"}}`)
	dir := t.TempDir()
	code, _, stderr := pull(t, srv, envPullInput{Dir: dir, Deployment: "shop"})
	if code == 0 {
		t.Fatal("viewer was served")
	}
	if !strings.Contains(stderr, "refused") || !strings.Contains(stderr, "secrets get") {
		t.Errorf("stderr: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.local")); err == nil {
		t.Error(".env.local written on refusal")
	}
}

func TestEnvPullHelpStatesTheModel(t *testing.T) {
	for _, want := range []string{"Values live in Dibbla, names live in the code", ".env.example", "real database", "secrets get"} {
		if !strings.Contains(envPullCmd.Long, want) {
			t.Errorf("--help lacks %q", want)
		}
	}
}
