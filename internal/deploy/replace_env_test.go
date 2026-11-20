package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// DIB-1835: the server keeps every stored variable a deploy does not name;
// --replace-env is the one way to say "these are all of them", and it travels
// as replace_env only when it is given.
func TestRun_ReplaceEnvTravelsOnlyWhenGiven(t *testing.T) {
	for _, c := range []struct {
		name string
		opts Options
		want string
	}{
		{"replace with env", Options{Env: []string{"A=1"}, ReplaceEnv: true}, "true"},
		{"replace alone", Options{ReplaceEnv: true}, "true"},
		{"env without replace", Options{Env: []string{"A=1"}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseMultipartForm(50 << 20)
				got = r.FormValue("replace_env")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"status":"success","deployment":{"id":"x","alias":"x","url":"x","status":"running","created_at":"2024-01-01T00:00:00Z","deployed_at":"2024-01-01T00:00:00Z"}}`))
			}))
			defer srv.Close()
			opts := c.opts
			opts.APIURL, opts.APIToken, opts.Path, opts.Alias = srv.URL, "tok", dir, "x"
			if _, err := Run(opts, nil); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != c.want {
				t.Errorf("replace_env = %q, want %q", got, c.want)
			}
		})
	}
}
