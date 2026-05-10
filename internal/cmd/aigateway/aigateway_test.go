package aigateway

import "testing"

func TestDeriveFromAPIURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"https://api.dibbla.com", "https://ai.dibbla.com", false},
		{"https://api.dibbla.net", "https://ai.dibbla.net", false},
		{"https://api.dibbla.com/", "https://ai.dibbla.com", false},
		{"https://api.dibbla.com/anything/here", "https://ai.dibbla.com", false},
		{"http://api.dibbla.local:8443", "http://ai.dibbla.local:8443", false},
		{"api.dibbla.com", "https://ai.dibbla.com", false}, // bare host gets https://
		{"http://localhost:8090", "", true},
		{"https://staging.dibbla.com", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := deriveFromAPIURL(c.in)
		if c.err {
			if err == nil {
				t.Errorf("deriveFromAPIURL(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("deriveFromAPIURL(%q) error = %v, want %q", c.in, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("deriveFromAPIURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveGatewayURL_EnvOverride(t *testing.T) {
	t.Setenv("DIBBLA_AI_GATEWAY_URL", "https://example.test/gw/")
	r := resolveGatewayURL()
	if r.URL != "https://example.test/gw" { // trailing slash stripped
		t.Errorf("URL = %q, want trailing slash stripped", r.URL)
	}
	if r.Source != "env (DIBBLA_AI_GATEWAY_URL)" {
		t.Errorf("Source = %q", r.Source)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":         "'plain'",
		"":              "''",
		"with space":    "'with space'",
		"it's":          `'it'\''s'`,
		"https://x?a=b": "'https://x?a=b'",
	}
	for in, want := range cases {
		got := shellQuote(in)
		if got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
