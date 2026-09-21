package export

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAPI is the slice of deploy-api an export talks to, for the app "shop"
// with one database, one bucket and a multi-service manifest in its source.
func fakeAPI(t *testing.T, vcsEnabled bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, `{"error":{"code":"UNAUTHORIZED"}}`, 401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/deploy/deployments/shop", auth(func(w http.ResponseWriter, r *http.Request) {
		port := 3000
		fmt.Fprintf(w, `{"alias":"shop","url":"https://shop.example","port":%d,"services":[{"name":"web","port":3000,"is_public":true,"is_built":true,"replicas":1},{"name":"worker","image":"ghcr.io/x/worker:1","replicas":1}]}`, port)
	}))
	mux.HandleFunc("GET /api/deploy/deployments/missing", auth(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"NOT_FOUND","message":"no such app"}}`, 404)
	}))
	mux.HandleFunc("GET /api/deploy/deployments/shop/vcs/info", auth(func(w http.ResponseWriter, r *http.Request) {
		if !vcsEnabled {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, 404)
			return
		}
		fmt.Fprint(w, `{"default_branch":"main","latest_sha":"0123456789abcdef","clone_url":"https://git.example/acme/shop.git"}`)
	}))
	mux.HandleFunc("GET /api/deploy/deployments/shop/env", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("service") == "worker" {
			fmt.Fprint(w, `{"deployment_alias":"shop","service":"worker","variables":[{"name":"API_KEY","value":"s3cret","source":"deployment"},{"name":"WORKER_TOKEN","value":"wt","source":"service"}]}`)
			return
		}
		fmt.Fprint(w, `{"deployment_alias":"shop","variables":[{"name":"API_KEY","value":"s3cret","source":"deployment"},{"name":"LOG_LEVEL","value":"debug","source":"inline"},{"name":"DATABASE_URL_SHOPDB","value":"postgres://x@db.dibbla/shopdb","source":"platform"},{"name":"STORAGE_UPLOADS_BUCKET","value":"uploads","source":"platform"},{"name":"GLOBAL_KEY","value":"g","source":"global"}]}`)
	}))
	mux.HandleFunc("GET /api/deploy/secrets", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("service") == "worker" {
			fmt.Fprint(w, `{"secrets":[{"name":"WORKER_TOKEN","deployment_alias":"shop","service_name":"worker"}],"total":1}`)
			return
		}
		fmt.Fprint(w, `{"secrets":[],"total":0}`)
	}))
	mux.HandleFunc("GET /api/deploy/databases/info", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"databases":[{"name":"shopdb","deployment_alias":"shop","size_bytes":10},{"name":"otherdb","deployment_alias":"other","size_bytes":10}]}`)
	}))
	mux.HandleFunc("GET /api/deploy/databases/shopdb/dump", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, "PGDMP-fake")
	}))
	mux.HandleFunc("GET /api/deploy/databases/otherdb/dump", auth(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("otherdb belongs to another app and must not be dumped")
	}))
	mux.HandleFunc("GET /api/deploy/buckets/info", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":[{"name":"uploads","deployment_alias":"shop","objects":3},{"name":"foreign","deployment_alias":"other"}],"total":2}`)
	}))
	objects := map[string]string{"a.txt": "A", "img/1.png": "PNG1", "../escape": "nope"}
	mux.HandleFunc("GET /api/deploy/buckets/uploads/objects", auth(func(w http.ResponseWriter, r *http.Request) {
		// Two pages: first "../escape" and "a.txt", then "img/1.png".
		if r.URL.Query().Get("start_after") == "" {
			fmt.Fprint(w, `{"bucket":"uploads","objects":[{"key":"../escape","size_bytes":4},{"key":"a.txt","size_bytes":1}],"truncated":true,"next_start_after":"a.txt"}`)
			return
		}
		fmt.Fprint(w, `{"bucket":"uploads","objects":[{"key":"img/1.png","size_bytes":4}],"truncated":false}`)
	}))
	mux.HandleFunc("GET /api/deploy/buckets/uploads/objects/{key...}", auth(func(w http.ResponseWriter, r *http.Request) {
		data, ok := objects[r.PathValue("key")]
		if !ok {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, 404)
			return
		}
		fmt.Fprint(w, data)
	}))
	mux.HandleFunc("GET /api/deploy/buckets/foreign/objects", auth(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("foreign bucket belongs to another app and must not be listed")
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeGit stands in for `git clone`: it writes a source tree with a
// dibbla.yaml into the destination and records the arguments.
func fakeGit(t *testing.T, manifestYAML string) (func(args ...string) error, *[][]string) {
	t.Helper()
	var calls [][]string
	return func(args ...string) error {
		calls = append(calls, args)
		dest := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(dest, "web"), 0o755); err != nil {
			return err
		}
		if manifestYAML != "" {
			if err := os.WriteFile(filepath.Join(dest, "dibbla.yaml"), []byte(manifestYAML), 0o644); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(dest, "web", "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	}, &calls
}

const shopManifest = `version: 1
services:
  web:
    build: ./web
    port: 3000
    public: true
  worker:
    image: ghcr.io/x/worker:1
    command: ["run", "--once"]
`

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestRun_FullExport(t *testing.T) {
	srv := fakeAPI(t, true)
	git, calls := fakeGit(t, shopManifest)
	out := filepath.Join(t.TempDir(), "shop-export")

	m, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Alias: "shop", OutDir: out, CLIVersion: "1.2.3", Git: git})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Source: a plain clone of the canonical URL — the token is never on
	// the command line (the credential helper answers for the host).
	if len(*calls) != 1 {
		t.Fatalf("git calls = %v", *calls)
	}
	args := strings.Join((*calls)[0], " ")
	if !strings.HasPrefix(args, "clone --quiet https://git.example/acme/shop.git ") {
		t.Errorf("git args = %q", args)
	}
	if strings.Contains(args, "tok") {
		t.Errorf("token leaked into the git invocation: %q", args)
	}
	if m.Source == nil || m.Source.Commit != "0123456789abcdef" {
		t.Errorf("source = %+v", m.Source)
	}

	// Manifest copied from the source.
	if m.ManifestOrigin != "source" || read(t, filepath.Join(out, "dibbla.yaml")) != shopManifest {
		t.Errorf("manifest origin = %s", m.ManifestOrigin)
	}

	// Databases: only the app's own, dumped as-is.
	if len(m.Databases) != 1 || m.Databases[0].Name != "shopdb" {
		t.Fatalf("databases = %+v", m.Databases)
	}
	if got := read(t, filepath.Join(out, "databases", "shopdb.dump")); got != "PGDMP-fake" {
		t.Errorf("dump = %q", got)
	}

	// Buckets: both pages fetched, nested keys become directories, an
	// escaping key is skipped and reported rather than written outside.
	if len(m.Buckets) != 1 || m.Buckets[0].Objects != 2 || m.Buckets[0].Bytes != 5 {
		t.Fatalf("buckets = %+v", m.Buckets)
	}
	if got := read(t, filepath.Join(out, "buckets", "uploads", "img", "1.png")); got != "PNG1" {
		t.Errorf("nested object = %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, "buckets", "escape")); err == nil {
		t.Error("escaping key was written outside the bucket directory")
	}
	if len(m.Buckets[0].Skipped) != 1 || m.Buckets[0].Skipped[0] != "../escape" {
		t.Errorf("skipped = %v", m.Buckets[0].Skipped)
	}

	// Env: secrets blanked, inline kept, platform values commented out.
	env := read(t, filepath.Join(out, "env", "app.env"))
	for _, want := range []string{"\nAPI_KEY=\n", "\nGLOBAL_KEY=\n", "\nLOG_LEVEL=debug\n", "# DATABASE_URL_SHOPDB=  (platform-generated", "# STORAGE_UPLOADS_BUCKET=  (platform-generated"} {
		if !strings.Contains(env, want) {
			t.Errorf("env/app.env lacks %q:\n%s", want, env)
		}
	}
	// A DATABASE_URL_* carries the role's password: it is a secret too.
	for _, leak := range []string{"s3cret", "postgres://x@db.dibbla/shopdb"} {
		if strings.Contains(env, leak) {
			t.Errorf("value %q exported without --include-secrets:\n%s", leak, env)
		}
	}
	worker := read(t, filepath.Join(out, "env", "worker.env"))
	if !strings.Contains(worker, "\nWORKER_TOKEN=\n") || strings.Contains(worker, "API_KEY") {
		t.Errorf("env/worker.env should carry only service-scoped names:\n%s", worker)
	}
	if m.Env.SecretsIncluded {
		t.Error("SecretsIncluded should be false")
	}

	// Compose: services from the manifest, wired to local postgres/minio.
	compose := read(t, filepath.Join(out, "docker-compose.yml"))
	for _, want := range []string{
		"  web:\n    build:\n      context: ./source/web\n",
		"  worker:\n    image: ghcr.io/x/worker:1\n",
		`command: ["run", "--once"]`,
		"DATABASE_URL_SHOPDB: postgres://postgres:postgres@postgres:5432/shopdb?sslmode=disable",
		"STORAGE_UPLOADS_ENDPOINT: http://minio:9000",
		"STORAGE_UPLOADS_BUCKET: uploads",
		"DIBBLA_SVC_WEB_URL: http://web:3000",
		"      - env/worker.env\n",
		"  postgres:\n    image: postgres:17-alpine",
		"mc mirror --overwrite /seed/uploads local/uploads",
		"      - \"8080:3000\"\n",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml lacks %q:\n%s", want, compose)
		}
	}
	restore := read(t, filepath.Join(out, "compose", "restore-databases.sh"))
	if !strings.Contains(restore, `pg_restore -U postgres --no-owner --no-privileges -d "shopdb" /dumps/shopdb.dump`) {
		t.Errorf("restore script:\n%s", restore)
	}

	// README and the JSON inventory.
	readme := read(t, filepath.Join(out, "README.md"))
	if !strings.Contains(readme, "docker compose up --build") || !strings.Contains(readme, "not** included") {
		t.Errorf("README:\n%s", readme)
	}
	var inv Manifest
	if err := json.Unmarshal([]byte(read(t, filepath.Join(out, "dibbla-export.json"))), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Format != Format || inv.App.Alias != "shop" || inv.CLIVersion != "1.2.3" || len(inv.Env.Variables) == 0 {
		t.Errorf("inventory = %+v", inv)
	}
	for _, v := range inv.Env.Variables {
		_ = v // names and sources only — the struct has no value field
	}
}

func TestRun_IncludeSecretsWritesValues(t *testing.T) {
	srv := fakeAPI(t, true)
	git, _ := fakeGit(t, shopManifest)
	out := filepath.Join(t.TempDir(), "x")
	m, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Alias: "shop", OutDir: out, IncludeSecrets: true, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Env.SecretsIncluded {
		t.Error("SecretsIncluded should be true")
	}
	env := read(t, filepath.Join(out, "env", "app.env"))
	if !strings.Contains(env, "\nAPI_KEY=s3cret\n") {
		t.Errorf("value missing:\n%s", env)
	}
	if !strings.Contains(env, "# DATABASE_URL_SHOPDB=postgres://x@db.dibbla/shopdb\n") {
		t.Errorf("platform value should be exported, commented out:\n%s", env)
	}
	if !strings.Contains(read(t, filepath.Join(out, "README.md")), "**are included**") {
		t.Error("README should warn that secrets are included")
	}
}

func TestRun_NoVersionControlGeneratesManifest(t *testing.T) {
	srv := fakeAPI(t, false)
	git, calls := fakeGit(t, "")
	out := filepath.Join(t.TempDir(), "x")
	m, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Alias: "shop", OutDir: out, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || m.Source != nil || m.SourceUnavailable == "" {
		t.Errorf("source should be recorded as unavailable: %+v (git calls %v)", m, *calls)
	}
	if m.ManifestOrigin != "generated" {
		t.Errorf("manifest origin = %s", m.ManifestOrigin)
	}
	manifest := read(t, filepath.Join(out, "dibbla.yaml"))
	if !strings.Contains(manifest, "  web:\n    build: .\n    port: 3000\n    public: true\n") || !strings.Contains(manifest, "  worker:\n    image: \"ghcr.io/x/worker:1\"\n") {
		t.Errorf("generated manifest:\n%s", manifest)
	}
	compose := read(t, filepath.Join(out, "docker-compose.yml"))
	if !strings.Contains(compose, "  web:\n    build:\n      context: ./source\n") {
		t.Errorf("compose from deployed config:\n%s", compose)
	}
	if !strings.Contains(read(t, filepath.Join(out, "README.md")), "not exported: version control is not enabled") {
		t.Error("README should say why source is missing")
	}
}

func TestRun_RefusesNonEmptyOutDir(t *testing.T) {
	srv := fakeAPI(t, true)
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Alias: "shop", OutDir: out})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_UnknownApp(t *testing.T) {
	srv := fakeAPI(t, true)
	_, err := Run(Options{APIURL: srv.URL, APIToken: "tok", Alias: "missing", OutDir: filepath.Join(t.TempDir(), "x")})
	if err == nil || !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Fatalf("err = %v", err)
	}
}

func TestSafeRelPath(t *testing.T) {
	for key, ok := range map[string]bool{
		"a.txt": true, "dir/sub/file": true, "../x": false, "a/../b": false, "/abs": false, "a//b": false, "": false, ".": false,
	} {
		if _, got := safeRelPath(key); got != ok {
			t.Errorf("safeRelPath(%q) ok = %v, want %v", key, got, ok)
		}
	}
}
