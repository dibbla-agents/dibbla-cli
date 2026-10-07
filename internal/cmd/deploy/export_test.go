package deploy

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportAPI is the smallest deploy-api an export needs: an app without
// version control, databases or buckets, and one secret in its environment.
func exportAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/deploy/deployments/tiny", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"alias":"tiny","url":"https://tiny.example","port":8000}`)
	})
	mux.HandleFunc("GET /api/deploy/deployments/tiny/vcs/info", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, 404)
	})
	mux.HandleFunc("GET /api/deploy/deployments/tiny/env", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"deployment_alias":"tiny","variables":[{"name":"LOG_LEVEL","value":"info","source":"inline"}],"secrets":[{"name":"TOKEN","source":"deployment"}]}`)
	})
	mux.HandleFunc("GET /api/deploy/databases/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"databases":[]}`)
	})
	mux.HandleFunc("GET /api/deploy/buckets/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":[],"total":0}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestExport_DefaultOutDirAndSummary(t *testing.T) {
	srv := exportAPI(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "tiny-export")
	var stdout, stderr bytes.Buffer
	code := runExportCore(&stdout, &stderr, exportInput{
		Alias: "tiny", Out: out, APIURL: srv.URL, APIToken: "tok",
	})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Exported tiny to", "source: not exported", "databases: 0, buckets: 0, env files: 1", "secret values are not part of an export"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(out, "dibbla-export.json")); err != nil {
		t.Errorf("inventory missing: %v", err)
	}
	env, _ := os.ReadFile(filepath.Join(out, "env", "app.env"))
	for _, want := range []string{"LOG_LEVEL=info\n", "# deployment secret; value not exported\nTOKEN=\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env/app.env lacks %q:\n%s", want, env)
		}
	}
}

// TestExport_IncludeSecretsIsRefused: secret values cannot be exported
// (DIB-1337). The flag is kept, hidden, so a script that passes it hears why,
// and nothing is written.
func TestExport_IncludeSecretsIsRefused(t *testing.T) {
	srv := exportAPI(t)
	out := filepath.Join(t.TempDir(), "x")
	var stdout, stderr bytes.Buffer
	code := runExportCore(&stdout, &stderr, exportInput{
		Alias: "tiny", Out: out, IncludeSecrets: true, APIURL: srv.URL, APIToken: "tok",
	})
	if code != 5 || !strings.Contains(stderr.String(), "cannot be exported") {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("output directory created for a refused export")
	}
	if f := exportCmd.Flags().Lookup("include-secrets"); f == nil || !f.Hidden {
		t.Error("--include-secrets should stay registered, hidden")
	}
}

func TestExport_InvalidAlias(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runExportCore(&stdout, &stderr, exportInput{Alias: "Not Valid!"}); code != 5 {
		t.Fatalf("exit %d", code)
	}
}
