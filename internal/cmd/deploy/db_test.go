package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubFetchProxyInfo returns ok=false; the caller falls back to derivation.
// Used by tests that want to exercise the derivation + env-override path
// without the 5s HTTP timeout from the real fetcher.
var stubFetchProxyInfoUnavailable dbProxyInfoFetcher = func(string, string) (dbProxyInfo, bool) {
	return dbProxyInfo{}, false
}

func TestDeriveDBHostAndSSLMode(t *testing.T) {
	tests := []struct {
		name         string
		apiURL       string
		wantHost     string
		wantSSLMode  string
	}{
		{"prod default", "https://api.dibbla.com", "db.dibbla.com", "require"},
		{"prod no scheme", "api.dibbla.com", "db.dibbla.com", "require"},
		{"prod trailing slash", "https://api.dibbla.com/", "db.dibbla.com", "require"},
		{"prod uppercase", "https://API.DIBBLA.COM", "db.dibbla.com", "require"},
		{"internal net", "https://api.dibbla.net", "db.dibbla.net", "disable"},
		{"internal net no scheme", "api.dibbla.net", "db.dibbla.net", "disable"},
		{"internal net with port", "https://api.dibbla.net:8443", "db.dibbla.net", "disable"},
		{"localhost", "http://localhost:8080", "db.dibbla.com", "disable"},
		{"127.0.0.1", "http://127.0.0.1:8080", "db.dibbla.com", "disable"},
		{"staging api.staging.dibbla.net", "https://api.staging.dibbla.net", "db.staging.dibbla.net", "require"},
		{"custom api", "https://api.example.com", "db.example.com", "require"},
		{"non-api host falls back", "https://myproxy.example.com", "db.dibbla.com", "require"},
		{"empty URL falls back", "", "db.dibbla.com", "require"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, sslmode := deriveDBHostAndSSLMode(tt.apiURL)
			if host != tt.wantHost {
				t.Errorf("host: got %q, want %q", host, tt.wantHost)
			}
			if sslmode != tt.wantSSLMode {
				t.Errorf("sslmode: got %q, want %q", sslmode, tt.wantSSLMode)
			}
		})
	}
}

func TestDBProxyEndpoint_Overrides(t *testing.T) {
	tests := []struct {
		name        string
		apiURL      string
		env         map[string]string
		wantHost    string
		wantPort    string
		wantSSLMode string
	}{
		{
			name:        "no overrides, prod",
			apiURL:      "https://api.dibbla.com",
			env:         nil,
			wantHost:    "db.dibbla.com",
			wantPort:    "30432",
			wantSSLMode: "require",
		},
		{
			name:        "no overrides, internal",
			apiURL:      "https://api.dibbla.net",
			env:         nil,
			wantHost:    "db.dibbla.net",
			wantPort:    "30432",
			wantSSLMode: "disable",
		},
		{
			name:        "DIBBLA_DB_HOST override",
			apiURL:      "https://api.dibbla.com",
			env:         map[string]string{"DIBBLA_DB_HOST": "custom-db.example.com"},
			wantHost:    "custom-db.example.com",
			wantPort:    "30432",
			wantSSLMode: "require",
		},
		{
			name:        "DIBBLA_DB_PORT override",
			apiURL:      "https://api.dibbla.com",
			env:         map[string]string{"DIBBLA_DB_PORT": "5432"},
			wantHost:    "db.dibbla.com",
			wantPort:    "5432",
			wantSSLMode: "require",
		},
		{
			name:        "DIBBLA_DB_SSLMODE override forces disable on prod",
			apiURL:      "https://api.dibbla.com",
			env:         map[string]string{"DIBBLA_DB_SSLMODE": "disable"},
			wantHost:    "db.dibbla.com",
			wantPort:    "30432",
			wantSSLMode: "disable",
		},
		{
			name:        "DIBBLA_DB_SSLMODE override forces require on internal",
			apiURL:      "https://api.dibbla.net",
			env:         map[string]string{"DIBBLA_DB_SSLMODE": "require"},
			wantHost:    "db.dibbla.net",
			wantPort:    "30432",
			wantSSLMode: "require",
		},
		{
			name:   "all three overrides",
			apiURL: "https://api.dibbla.com",
			env: map[string]string{
				"DIBBLA_DB_HOST":    "10.0.0.5",
				"DIBBLA_DB_PORT":    "15432",
				"DIBBLA_DB_SSLMODE": "verify-full",
			},
			wantHost:    "10.0.0.5",
			wantPort:    "15432",
			wantSSLMode: "verify-full",
		},
		{
			name:        "empty env var is ignored",
			apiURL:      "https://api.dibbla.net",
			env:         map[string]string{"DIBBLA_DB_HOST": ""},
			wantHost:    "db.dibbla.net",
			wantPort:    "30432",
			wantSSLMode: "disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string {
				if tt.env == nil {
					return ""
				}
				return tt.env[k]
			}
			host, port, sslmode := dbProxyEndpointWith(tt.apiURL, "tkn", getenv, stubFetchProxyInfoUnavailable)
			if host != tt.wantHost {
				t.Errorf("host: got %q, want %q", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("port: got %q, want %q", port, tt.wantPort)
			}
			if sslmode != tt.wantSSLMode {
				t.Errorf("sslmode: got %q, want %q", sslmode, tt.wantSSLMode)
			}
		})
	}
}

// TestDBProxyEndpoint_APIPreferred — API-served values beat the derivation
// fallback, while env vars still override both.
func TestDBProxyEndpoint_APIPreferred(t *testing.T) {
	apiInfo := func(_, _ string) (dbProxyInfo, bool) {
		return dbProxyInfo{Host: "dbproxy.cluster.local", Port: "5432", SSLMode: "disable"}, true
	}
	host, port, sslmode := dbProxyEndpointWith("https://api.dibbla.com", "tkn", func(string) string { return "" }, apiInfo)
	if host != "dbproxy.cluster.local" || port != "5432" || sslmode != "disable" {
		t.Errorf("api values should win over derivation: got %s:%s sslmode=%s", host, port, sslmode)
	}

	// Env override wins over API.
	host, port, _ = dbProxyEndpointWith(
		"https://api.dibbla.com",
		"tkn",
		func(k string) string {
			if k == "DIBBLA_DB_PORT" {
				return "9999"
			}
			return ""
		},
		apiInfo,
	)
	if port != "9999" {
		t.Errorf("env override should win over API: got port=%s", port)
	}
	if host != "dbproxy.cluster.local" {
		t.Errorf("non-overridden field should still come from API: got host=%s", host)
	}
}

// TestFetchDBProxyInfoOverHTTP — the production fetcher correctly parses a
// real /db/proxy-info response and returns ok=false on common failure modes.
func TestFetchDBProxyInfoOverHTTP(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/deploy/db/proxy-info" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(dbProxyInfo{Host: "h", Port: "1234", SSLMode: "disable"})
		}))
		defer srv.Close()
		info, ok := fetchDBProxyInfoOverHTTP(srv.URL, "tkn")
		if !ok || info.Host != "h" || info.Port != "1234" || info.SSLMode != "disable" {
			t.Errorf("happy path: ok=%v info=%+v", ok, info)
		}
	})
	t.Run("404 falls back", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		}))
		defer srv.Close()
		if _, ok := fetchDBProxyInfoOverHTTP(srv.URL, "tkn"); ok {
			t.Errorf("404 should produce ok=false")
		}
	})
	t.Run("malformed body falls back", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("not json"))
		}))
		defer srv.Close()
		if _, ok := fetchDBProxyInfoOverHTTP(srv.URL, "tkn"); ok {
			t.Errorf("malformed body should produce ok=false")
		}
	})
	t.Run("missing required fields falls back", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(dbProxyInfo{Host: "", Port: "5432"})
		}))
		defer srv.Close()
		if _, ok := fetchDBProxyInfoOverHTTP(srv.URL, "tkn"); ok {
			t.Errorf("empty host should produce ok=false")
		}
	})
	t.Run("empty apiURL returns ok=false", func(t *testing.T) {
		if _, ok := fetchDBProxyInfoOverHTTP("", "tkn"); ok {
			t.Errorf("empty url should produce ok=false")
		}
	})
	t.Run("auth header is sent", func(t *testing.T) {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(dbProxyInfo{Host: "h", Port: "p", SSLMode: "disable"})
		}))
		defer srv.Close()
		fetchDBProxyInfoOverHTTP(srv.URL, "secret-token")
		if seen != "Bearer secret-token" {
			t.Errorf("auth header: got %q, want Bearer secret-token", seen)
		}
	})
}

func TestParseAPIHost(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://api.dibbla.com", "api.dibbla.com"},
		{"api.dibbla.net", "api.dibbla.net"},
		{"https://api.dibbla.net:8443", "api.dibbla.net"},
		{"  https://API.DIBBLA.NET  ", "api.dibbla.net"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := parseAPIHost(tt.in)
			if got != tt.want {
				t.Errorf("parseAPIHost(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
