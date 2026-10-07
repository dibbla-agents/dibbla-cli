package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// DIB-1339: --allow-secret-env is the person's "these are not secrets". It
// travels as allow_secret_shaped_env, only with env vars to cover, and never
// on its own.
func TestRun_AllowSecretEnvTravelsOnlyWithEnvVars(t *testing.T) {
	cases := []struct {
		name  string
		opts  Options
		field string
	}{
		{"flag with env", Options{Env: []string{"MAPS_KEY=x"}, AllowSecretEnv: true}, "true"},
		{"env without flag", Options{Env: []string{"MAPS_KEY=x"}}, ""},
		{"flag without env", Options{AllowSecretEnv: true}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
			var got string
			var sent bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseMultipartForm(50 << 20)
				_, sent = r.MultipartForm.Value["allow_secret_shaped_env"]
				got = r.FormValue("allow_secret_shaped_env")
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
			if got != c.field || sent != (c.field != "") {
				t.Errorf("allow_secret_shaped_env = %q (sent %v), want %q", got, sent, c.field)
			}
		})
	}
}
