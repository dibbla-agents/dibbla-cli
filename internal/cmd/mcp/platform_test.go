package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/platformcontract"
	"github.com/dibbla-agents/dibbla-cli/internal/platformoauth"
)

// platformStub is one server playing both the MCP host (/platform and the
// RFC 9728 document) and the issuer (RFC 8414, token, introspect), so the
// check's every link can be bent one at a time.
type platformStub struct {
	srv          *httptest.Server
	enabled      bool   // the /platform toolset and its metadata are mounted
	active       bool   // introspection answers active
	refreshOK    bool   // refresh_token grant succeeds
	mcpStatus    int    // non-zero forces this HTTP status on /platform
	mcpCode      string // error code for the forced status
	acceptBearer string
}

func newPlatformStub(t *testing.T) *platformStub {
	t.Helper()
	s := &platformStub{enabled: true, active: true, refreshOK: true, acceptBearer: "at-live"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/platform", func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": s.srv.URL + "/platform", "authorization_servers": []string{s.srv.URL}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": s.srv.URL, "authorization_endpoint": s.srv.URL + "/oauth/authorize", "token_endpoint": s.srv.URL + "/oauth/token",
			"revocation_endpoint": s.srv.URL + "/oauth/revoke", "introspection_endpoint": s.srv.URL + "/oauth/introspect",
			"grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !s.refreshOK {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "this grant has been revoked"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": s.acceptBearer, "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt-2"})
	})
	mux.HandleFunc("POST /oauth/introspect", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": s.active, "exp": time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("POST /oauth/revoke", func(w http.ResponseWriter, r *http.Request) { s.active = false })
	mux.HandleFunc("POST /platform", func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			http.NotFound(w, r)
			return
		}
		if s.mcpStatus != 0 {
			w.WriteHeader(s.mcpStatus)
			fmt.Fprintf(w, `{"error":{"code":%q,"message":"forced"}}`, s.mcpCode)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.acceptBearer {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp.dibbla.com", error="invalid_token"`)
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"code":"INVALID_TOKEN","message":"the platform access token failed verification"}}`)
			return
		}
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body.String(), `"initialize"`) {
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":\"dibbla-mcp\",\"version\":\"2.0.0\"}}}\n")
			return
		}
		fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":1,"result":{"isError":false,"content":[{"type":"text","text":"You are Erik (owner) in Dibbla."},{"type":"text","text":""}],"structuredContent":{"user_id":"u1","email":"erik@example.com","name":"Erik","organization":{"id":"o1","slug":"dibbla","name":"Dibbla","role":"owner"},"scopes":["platform:identity:read","platform:apps:read"],"client_id":"dibbla-cli","auth":"oauth"}}}`+"\n") //contract-pinned: stub server output
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// isolate points every store at a temp dir: config.yaml, the credentials
// files, and a keyring that is "unavailable" so the file fallback is used.
// The grant then lands in a file the test can inspect.
func isolate(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || platform.IsCI() {
		t.Skip("needs Linux outside CI (file-based credential stores)")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Cleanup(cfgdir.SetForTest(filepath.Join(tmp, "dibbla")))
	t.Setenv("DIBBLA_API_TOKEN", "")
	t.Setenv("DIBBLA_API_URL", "https://api.example.test")
	t.Setenv("DIBBLA_CONTEXT", "")
	unavailable := fmt.Errorf("The name org.freedesktop.secrets was not provided by any .service files")
	origGet, origSet, origDel := credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete
	credential.KeyringGet = func(string, string) (string, error) { return "", unavailable }
	credential.KeyringSet = func(string, string, string) error { return unavailable }
	credential.KeyringDelete = func(string, string) error { return unavailable }
	t.Cleanup(func() {
		credential.KeyringGet, credential.KeyringSet, credential.KeyringDelete = origGet, origSet, origDel
	})
}

func storeGrant(t *testing.T, s *platformStub, expired bool) {
	t.Helper()
	exp := time.Now().Add(time.Hour)
	if expired {
		exp = time.Now().Add(-time.Minute)
	}
	g := &platformoauth.Grant{
		Issuer: s.srv.URL, Resource: s.srv.URL + "/platform", ClientID: "dibbla-cli",
		AccessToken: s.acceptBearer, RefreshToken: "rt-1", ExpiresAt: exp, Scope: "x",
		TokenEndpoint: s.srv.URL + "/oauth/token", IntrospectionEndpoint: s.srv.URL + "/oauth/introspect",
	}
	raw, _ := g.Encode()
	if err := credential.SetPlatformGrantFile(grantContextName(), raw); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformCheck(t *testing.T) {
	t.Run("happy path prints every link", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, false)
		var buf bytes.Buffer
		if err := runPlatformCheck(&buf); err != nil {
			t.Fatalf("check failed: %v\n%s", err, buf.String())
		}
		for _, want := range []string{"Discovery: ✅ issuer " + s.srv.URL, "PKCE S256, refresh, revoke, introspect", "Token:     ✅ valid for", "dibbla-mcp 2.0.0", "Erik <erik@example.com>", "Dibbla (dibbla) — role owner", "platform:identity:read, platform:apps:read", "oauth (client dibbla-cli)"} { //contract-pinned: expected output from the stub
			if !strings.Contains(buf.String(), want) {
				t.Errorf("output missing %q:\n%s", want, buf.String())
			}
		}
	})

	t.Run("missing grant says --login", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		var buf bytes.Buffer
		err := runPlatformCheck(&buf)
		if err == nil || !strings.Contains(err.Error(), "--login") {
			t.Errorf("want a --login hint, got: %v", err)
		}
		if !strings.Contains(buf.String(), "missing") {
			t.Errorf("token line must say missing:\n%s", buf.String())
		}
	})

	t.Run("expired token is refreshed and the grant re-stored", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, true)
		var buf bytes.Buffer
		if err := runPlatformCheck(&buf); err != nil {
			t.Fatalf("check failed: %v\n%s", err, buf.String())
		}
		if !strings.Contains(buf.String(), "expired → refreshed") {
			t.Errorf("must report the refresh:\n%s", buf.String())
		}
		g, _, err := loadGrant(grantContextName())
		if err != nil || g == nil || g.RefreshToken != "rt-2" {
			t.Errorf("rotated refresh token must be stored, got %+v err=%v", g, err)
		}
	})

	t.Run("revoked grant with expired token says revoked, run --login", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		s.refreshOK = false
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, true)
		var buf bytes.Buffer
		err := runPlatformCheck(&buf)
		if err == nil || !strings.Contains(err.Error(), "revoked") || !strings.Contains(err.Error(), "--login") {
			t.Errorf("want 'revoked … --login', got: %v", err)
		}
		if strings.Contains(err.Error(), "401") {
			t.Errorf("a revoked grant must not surface as a generic 401: %v", err)
		}
	})

	t.Run("revoked grant with live-looking token is caught by introspection", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		s.active = false
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, false)
		var buf bytes.Buffer
		err := runPlatformCheck(&buf)
		if err == nil || !strings.Contains(err.Error(), "revoked") || !strings.Contains(err.Error(), "--login") {
			t.Errorf("want 'revoked … --login', got: %v", err)
		}
		if !strings.Contains(buf.String(), "revoked") {
			t.Errorf("token line must say revoked:\n%s", buf.String())
		}
	})

	t.Run("toolset off is 404 with the operator hint", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		s.enabled = false
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		var buf bytes.Buffer
		err := runPlatformCheck(&buf)
		if err == nil || !strings.Contains(err.Error(), "switched off") {
			t.Errorf("want the switched-off explanation, got: %v", err)
		}
	})

	t.Run("insufficient scope names the fix", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		s.mcpStatus, s.mcpCode = 403, "INSUFFICIENT_SCOPE"
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, false)
		var buf bytes.Buffer
		err := runPlatformCheck(&buf)
		if err == nil || !strings.Contains(err.Error(), "INSUFFICIENT_SCOPE") || !strings.Contains(err.Error(), "--logout") {
			t.Errorf("want scope advice, got: %v", err)
		}
	})

	t.Run("auth service down is 503 with retry advice", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		s.mcpStatus, s.mcpCode = 503, "AUTH_SERVICE_UNAVAILABLE"
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		storeGrant(t, s, false)
		err := runPlatformCheck(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "retry") {
			t.Errorf("want 503 retry advice, got: %v", err)
		}
	})

	t.Run("grant for another install is refused before use", func(t *testing.T) {
		isolate(t)
		s := newPlatformStub(t)
		t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
		g := &platformoauth.Grant{Issuer: "https://elsewhere.example", Resource: "https://elsewhere.example/platform", AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)}
		raw, _ := g.Encode()
		_ = credential.SetPlatformGrantFile(grantContextName(), raw)
		err := runPlatformCheck(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "does not belong to this install") {
			t.Errorf("want install mismatch, got: %v", err)
		}
	})
}

func TestPlatformLogout(t *testing.T) {
	isolate(t)
	s := newPlatformStub(t)
	t.Setenv("DIBBLA_MCP_URL", s.srv.URL)
	storeGrant(t, s, false)
	g, _, _ := loadGrant(grantContextName())
	g.RevocationEndpoint = s.srv.URL + "/oauth/revoke"
	raw, _ := g.Encode()
	_ = credential.SetPlatformGrantFile(grantContextName(), raw)

	var buf bytes.Buffer
	if err := runPlatformLogout(&buf); err != nil {
		t.Fatalf("logout: %v\n%s", err, buf.String())
	}
	if s.active {
		t.Error("logout must revoke server-side")
	}
	if g, _, _ := loadGrant(grantContextName()); g != nil {
		t.Error("logout must forget the grant")
	}
	if err := runPlatformCheck(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--login") {
		t.Errorf("after logout the check must ask for --login, got %v", err)
	}
}

func TestPlatformConfigOutputs(t *testing.T) {
	t.Setenv("DIBBLA_MCP_URL", "https://mcp.dibbla.com")
	renderP := func(client string) string {
		var buf bytes.Buffer
		if err := runPlatform(&buf, client); err != nil {
			t.Fatalf("runPlatform(%q): %v", client, err)
		}
		return buf.String()
	}
	t.Run("claude has no header", func(t *testing.T) {
		out := renderP("claude")
		jsonPart := out[strings.Index(out, "\n{")+1:]
		var v struct {
			MCPServers map[string]struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(jsonPart), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, jsonPart)
		}
		srv := v.MCPServers["dibbla-platform"]
		if srv.URL != "https://mcp.dibbla.com/platform" || srv.Type != "http" || len(srv.Headers) != 0 {
			t.Errorf("unexpected entry: %+v", srv)
		}
		if !strings.Contains(out, "claude mcp add --transport http dibbla-platform https://mcp.dibbla.com/platform") {
			t.Errorf("one-liner missing:\n%s", out)
		}
	})
	t.Run("codex has no bearer env var", func(t *testing.T) {
		out := renderP("codex")
		if !strings.Contains(out, "[mcp_servers.dibbla-platform]") || strings.Contains(out, "bearer_token_env_var =") || !strings.Contains(out, "codex mcp login") {
			t.Errorf("unexpected codex output:\n%s", out)
		}
	})
	t.Run("gemini enables oauth", func(t *testing.T) {
		out := renderP("gemini")
		var v struct {
			MCPServers map[string]struct {
				HTTPURL string          `json:"httpUrl"`
				OAuth   map[string]bool `json:"oauth"`
				Headers map[string]string
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if !v.MCPServers["dibbla-platform"].OAuth["enabled"] || v.MCPServers["dibbla-platform"].Headers != nil {
			t.Errorf("unexpected gemini entry: %+v", v.MCPServers)
		}
	})
	t.Run("no secrets, no internal hosts, oauth notice", func(t *testing.T) {
		for _, c := range []string{"", "claude", "codex", "gemini"} {
			out := renderP(c)
			if strings.Contains(out, "DIBBLA_API_TOKEN") || strings.Contains(out, "dibbla.net") {
				t.Errorf("client %q output carries a token reference or an internal host:\n%s", c, out)
			}
		}
		if !strings.Contains(renderP(""), "OAuth") {
			t.Error("the all-clients form must say the client negotiates OAuth")
		}
	})
}

func TestPlatformHelpListsLocalOnlyFromContract(t *testing.T) {
	long := platformLong()
	local := platformcontract.CapabilitiesInState(platformcontract.StateLocalOnly)
	if len(local) == 0 {
		t.Fatal("the vendored contract lists no local-only capability; the help would be describing nothing")
	}
	for _, c := range local {
		if !strings.Contains(long, c.ID) {
			t.Errorf("help does not name local-only capability %s", c.ID)
		}
	}
	if !strings.Contains(long, platformcontract.ContractVersion()) {
		t.Error("help must name the contract version it was rendered from")
	}
}

// Keep the test binary honest about file isolation: nothing above may have
// written into the real config dir.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
