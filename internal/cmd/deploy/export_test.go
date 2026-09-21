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
		fmt.Fprint(w, `{"deployment_alias":"tiny","variables":[{"name":"TOKEN","value":"hunter2","source":"deployment"}]}`)
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
		Alias: "tiny", Out: out, APIURL: srv.URL, APIToken: "tok", Interactive: true,
	})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Exported tiny to", "source: not exported", "databases: 0, buckets: 0, env files: 1", "secret values were not exported"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(out, "dibbla-export.json")); err != nil {
		t.Errorf("inventory missing: %v", err)
	}
	env, _ := os.ReadFile(filepath.Join(out, "env", "app.env"))
	if strings.Contains(string(env), "hunter2") {
		t.Error("secret value written without --include-secrets")
	}
}

func TestExport_IncludeSecretsNeedsConfirmation(t *testing.T) {
	srv := exportAPI(t)

	t.Run("non-interactive without --yes is refused, nothing written", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x")
		var stdout, stderr bytes.Buffer
		code := runExportCore(&stdout, &stderr, exportInput{
			Alias: "tiny", Out: out, IncludeSecrets: true, APIURL: srv.URL, APIToken: "tok", Interactive: false,
		})
		if code != 5 || !strings.Contains(stderr.String(), "--yes") {
			t.Fatalf("exit %d, stderr=%s", code, stderr.String())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Error("output directory created before confirmation")
		}
	})

	t.Run("declined prompt cancels", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x")
		var stdout, stderr bytes.Buffer
		code := runExportCore(&stdout, &stderr, exportInput{
			Alias: "tiny", Out: out, IncludeSecrets: true, APIURL: srv.URL, APIToken: "tok", Interactive: true,
			Confirm: func() (bool, error) { return false, nil },
		})
		if code != 5 || !strings.Contains(stdout.String(), "Cancelled") {
			t.Fatalf("exit %d, stdout=%s", code, stdout.String())
		}
	})

	t.Run("confirmed writes values", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x")
		var stdout, stderr bytes.Buffer
		asked := false
		code := runExportCore(&stdout, &stderr, exportInput{
			Alias: "tiny", Out: out, IncludeSecrets: true, APIURL: srv.URL, APIToken: "tok", Interactive: true,
			Confirm: func() (bool, error) { asked = true; return true, nil },
		})
		if code != 0 || !asked {
			t.Fatalf("exit %d asked=%v: %s", code, asked, stderr.String())
		}
		env, _ := os.ReadFile(filepath.Join(out, "env", "app.env"))
		if !strings.Contains(string(env), "TOKEN=hunter2") {
			t.Errorf("value missing:\n%s", env)
		}
		if !strings.Contains(stdout.String(), "password file") {
			t.Error("summary should warn about secret values on disk")
		}
	})

	t.Run("--yes skips the prompt", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x")
		var stdout, stderr bytes.Buffer
		code := runExportCore(&stdout, &stderr, exportInput{
			Alias: "tiny", Out: out, IncludeSecrets: true, Yes: true, APIURL: srv.URL, APIToken: "tok", Interactive: false,
			Confirm: func() (bool, error) { t.Fatal("prompt shown despite --yes"); return false, nil },
		})
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
	})
}

func TestExport_InvalidAlias(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runExportCore(&stdout, &stderr, exportInput{Alias: "Not Valid!"}); code != 5 {
		t.Fatalf("exit %d", code)
	}
}
