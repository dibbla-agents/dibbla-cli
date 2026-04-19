package deploy

import "testing"

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
			host, port, sslmode := dbProxyEndpoint(tt.apiURL, getenv)
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
