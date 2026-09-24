package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answerExposed makes the exposure lookup answer from a list (or fail) for
// the duration of one test, so name validation can be exercised without an
// API server.
func answerExposed(t *testing.T, names []string, err error) {
	t.Helper()
	orig := exposedServerNames
	exposedServerNames = func() ([]string, error) { return names, err }
	t.Cleanup(func() { exposedServerNames = orig })
}

func TestServerNameValidation(t *testing.T) {
	t.Run("valid names pass without asking the API", func(t *testing.T) {
		answerExposed(t, nil, errors.New("must not be called"))
		for _, name := range []string{"fatshark-image-gen", "org-test-worker", "a", "A.b_c-1", strings.Repeat("x", 64)} {
			if err := validateServerName(name); err != nil {
				t.Errorf("validateServerName(%q) = %v, want nil", name, err)
			}
		}
	})

	t.Run("an exposed server with an unaddressable name says rename it", func(t *testing.T) {
		answerExposed(t, []string{"acme tools", "fine-server"}, nil)
		err := validateServerName("acme tools")
		if err == nil {
			t.Fatal("want an error")
		}
		msg := err.Error()
		for _, want := range []string{"cannot be used as an MCP address", "WithServerName", `"acme tools"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("message missing %q:\n%s", want, msg)
			}
		}
		// No platform fallback is promised: such a name cannot be invoked
		// anywhere today (DIB-1075).
		if strings.Contains(msg, "mcp platform") {
			t.Errorf("must not offer `mcp platform` as a fallback:\n%s", msg)
		}
	})

	t.Run("an unknown invalid name is the plain invalid-name error", func(t *testing.T) {
		answerExposed(t, []string{"fine-server"}, nil)
		for _, name := range []string{"acme tools", "a/b", "", strings.Repeat("x", 65), "nö"} {
			err := validateServerName(name)
			if err == nil {
				t.Errorf("validateServerName(%q) = nil, want error", name)
				continue
			}
			if !strings.Contains(err.Error(), "invalid tool server name") || !strings.Contains(err.Error(), "dibbla functions exposed") {
				t.Errorf("validateServerName(%q) = %v, want the invalid-name error naming `dibbla functions exposed`", name, err)
			}
			if strings.Contains(err.Error(), "WithServerName") {
				t.Errorf("validateServerName(%q) must not claim the server exists", name)
			}
		}
	})

	t.Run("a failing lookup is 'cannot tell', not 'not exposed'", func(t *testing.T) {
		answerExposed(t, nil, errors.New("no API token"))
		err := validateServerName("acme tools")
		if err == nil || !strings.Contains(err.Error(), "invalid tool server name") {
			t.Errorf("want the plain invalid-name error, got %v", err)
		}
	})
}

func TestServerArgs(t *testing.T) {
	t.Run("no name names `dibbla functions exposed`", func(t *testing.T) {
		err := serverCmd.Args(serverCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "dibbla functions exposed") || !strings.Contains(err.Error(), "<tool-server-name>") {
			t.Errorf("want the how-to-find-names error, got %v", err)
		}
	})
	t.Run("two names says quote", func(t *testing.T) {
		err := serverCmd.Args(serverCmd, []string{"acme", "tools"})
		if err == nil || !strings.Contains(err.Error(), "quote") {
			t.Errorf("want the quoting hint, got %v", err)
		}
	})
	t.Run("one name is accepted", func(t *testing.T) {
		if err := serverCmd.Args(serverCmd, []string{"acme"}); err != nil {
			t.Errorf("got %v", err)
		}
	})
}

func TestServerHelpSaysWhatItIsNot(t *testing.T) {
	for _, want := range []string{
		"never starts or runs a server",
		"Only exposed functions appear",
		"dibbla functions exposed",
		"separate tools",
		"CONNECT BEFORE YOU LOG IN",
	} {
		if !strings.Contains(serverCmd.Long, want) {
			t.Errorf("server --help missing %q", want)
		}
	}
	if !strings.HasPrefix(serverCmd.Use, "server <tool-server-name>") {
		t.Errorf("usage line = %q", serverCmd.Use)
	}
	for _, want := range []string{
		"server <name>  one tool server's exposed functions as separate tools",
		"platform       all exposed functions through one tool",
		"none of them runs a server",
	} {
		if !strings.Contains(mcpCmd.Long, want) {
			t.Errorf("mcp --help missing %q", want)
		}
	}
}

func TestServerConfigOutputs(t *testing.T) {
	t.Setenv("DIBBLA_MCP_URL", "https://mcp.dibbla.com")
	answerExposed(t, nil, errors.New("must not be called"))
	const name = "fatshark-image-gen"
	const wantURL = "https://mcp.dibbla.com/platform/servers/" + name
	const wantKey = "dibbla-" + name
	renderS := func(client string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := runServer(&buf, name, client); err != nil {
			t.Fatalf("runServer(%q): %v", client, err)
		}
		return buf.String()
	}
	jsonFrom := func(out string) string {
		i := strings.Index(out, "{")
		j := strings.LastIndex(out, "}")
		return out[i : j+1]
	}

	t.Run("claude connects before it logs in", func(t *testing.T) {
		out := renderS("claude")
		var v struct {
			MCPServers map[string]struct {
				Type  string            `json:"type"`
				URL   string            `json:"url"`
				OAuth map[string]string `json:"oauth"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(jsonFrom(out)), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		srv := v.MCPServers[wantKey]
		if srv.URL != wantURL || srv.Type != "http" || srv.OAuth["scopes"] != agentScopes() {
			t.Errorf("unexpected entry: %+v", srv)
		}
		if !strings.Contains(out, "claude mcp add --transport http "+wantKey+" "+wantURL) {
			t.Errorf("one-liner missing:\n%s", out)
		}
		// The order is the point (DIB-1068 trap): add, connect, then log in.
		add := strings.Index(out, "claude mcp add")
		connect := strings.Index(out, "claude mcp list")
		login := strings.Index(out, "claude mcp login")
		if !(add < connect && connect < login) {
			t.Errorf("steps must be add < connect < login, got %d %d %d:\n%s", add, connect, login, out)
		}
		if !strings.Contains(out, "BEFORE any login") {
			t.Errorf("must say to connect before login:\n%s", out)
		}
	})

	t.Run("codex", func(t *testing.T) {
		out := renderS("codex")
		for _, want := range []string{"[mcp_servers." + wantKey + "]", `url = "` + wantURL + `"`, "codex mcp login " + wantKey} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "bearer_token_env_var =") {
			t.Errorf("OAuth form must not name a bearer env var:\n%s", out)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		out := renderS("gemini")
		var v struct {
			MCPServers map[string]struct {
				HTTPURL string          `json:"httpUrl"`
				OAuth   map[string]bool `json:"oauth"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(jsonFrom(out)), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if v.MCPServers[wantKey].HTTPURL != wantURL || !v.MCPServers[wantKey].OAuth["enabled"] {
			t.Errorf("unexpected entry: %+v", v.MCPServers)
		}
	})

	t.Run("cursor", func(t *testing.T) {
		out := renderS("cursor")
		var v struct {
			MCPServers map[string]struct {
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(jsonFrom(out)), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if v.MCPServers[wantKey].URL != wantURL || len(v.MCPServers[wantKey].Headers) != 0 {
			t.Errorf("unexpected entry: %+v", v.MCPServers)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		out := renderS("opencode")
		var v struct {
			MCP map[string]struct {
				Type    string `json:"type"`
				URL     string `json:"url"`
				Enabled bool   `json:"enabled"`
			} `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(jsonFrom(out)), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		e := v.MCP[wantKey]
		if e.Type != "remote" || e.URL != wantURL || !e.Enabled {
			t.Errorf("unexpected entry: %+v", e)
		}
		if !strings.Contains(out, "opencode mcp auth "+wantKey) {
			t.Errorf("missing the auth command:\n%s", out)
		}
	})

	t.Run("all clients, no secrets, no internal hosts, no login-first", func(t *testing.T) {
		for _, client := range []string{"", "claude", "codex", "gemini", "cursor", "opencode"} {
			out := renderS(client)
			if strings.Contains(out, "ak_") || strings.Contains(out, "DIBBLA_API_TOKEN") {
				t.Errorf("client %q output carries a token:\n%s", client, out)
			}
			if strings.Contains(out, "dibbla.net") {
				t.Errorf("client %q output names an internal host", client)
			}
			if !strings.Contains(out, wantURL) {
				t.Errorf("client %q output lacks the address", client)
			}
			// Every form ends with the check, because "Connected" with 0
			// tools is the one failure a client cannot show.
			if strings.Count(out, "dibbla mcp server "+name+" --check") != 1 || !strings.Contains(out, "0 tools = wrong server name, or nothing exposed") {
				t.Errorf("client %q output must end with exactly one verify hint:\n%s", client, out)
			}
		}
		all := renderS("")
		for _, section := range []string{"## Claude Code", "## Codex CLI", "## Gemini CLI", "## Cursor", "## opencode"} {
			if !strings.Contains(all, section) {
				t.Errorf("combined output missing %q", section)
			}
		}
	})

	t.Run("unknown client errors and names the five", func(t *testing.T) {
		var buf bytes.Buffer
		err := runServer(&buf, name, "zed")
		if err == nil || !strings.Contains(err.Error(), "cursor, or opencode") {
			t.Errorf("want the five-client error, got %v", err)
		}
	})
}

// TestExistingCommandsStillRefuseTheNewClients is the strictly-additive
// guarantee: cursor and opencode are `mcp server` clients only. The existing
// printer's wording is pinned so a shared-code change would show here.
func TestExistingCommandsStillRefuseTheNewClients(t *testing.T) {
	t.Setenv("DIBBLA_MCP_URL", "https://mcp.dibbla.com")
	for _, client := range []string{"cursor", "opencode"} {
		for cmd, run := range map[string]func(io.Writer, string) error{"platform": runPlatform, "community": runCommunity} {
			err := run(&bytes.Buffer{}, client)
			want := fmt.Sprintf("unknown --client %q (expected claude, codex, or gemini)", client)
			if err == nil || err.Error() != want {
				t.Errorf("mcp %s --client %s: got %v, want %q", cmd, client, err, want)
			}
		}
	}
}

// serverStub is an MCP host serving /platform/servers/{name} the way
// mcp-server does after DIB-1068: a known name with exposures lists tools, an
// unknown name is the empty server, and with the flag off everything is 404.
type serverStub struct {
	srv     *httptest.Server
	enabled bool
	tools   map[string][]string
}

func newServerStub(t *testing.T) *serverStub {
	t.Helper()
	s := &serverStub{enabled: true, tools: map[string][]string{"org-test-worker": {"word_stats", "todays_date"}}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /platform/servers/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer at-live" {
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
		var tools []map[string]string
		for _, n := range s.tools[r.PathValue("name")] {
			tools = append(tools, map[string]string{"name": n})
		}
		if tools == nil {
			tools = []map[string]string{}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"tools": tools}})
		fmt.Fprintf(w, "data: %s\n", raw)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func TestProbeServerTools(t *testing.T) {
	t.Run("lists the tools of a known server", func(t *testing.T) {
		s := newServerStub(t)
		ts := serverToolset("org-test-worker")
		got, err := probeServerTools(ts, s.srv.URL+ts.Path, "at-live")
		if err != nil {
			t.Fatal(err)
		}
		if got.ServerName != "dibbla-mcp" || strings.Join(got.Tools, ",") != "todays_date,word_stats" {
			t.Errorf("unexpected probe: %+v", got)
		}
	})
	t.Run("an unknown server is the empty server, not an error", func(t *testing.T) {
		s := newServerStub(t)
		ts := serverToolset("nobody")
		got, err := probeServerTools(ts, s.srv.URL+ts.Path, "at-live")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Tools) != 0 {
			t.Errorf("want no tools, got %v", got.Tools)
		}
	})
	t.Run("flag off is 404 with the SERVERS_MCP_ENABLED hint", func(t *testing.T) {
		s := newServerStub(t)
		s.enabled = false
		ts := serverToolset("org-test-worker")
		_, err := probeServerTools(ts, s.srv.URL+ts.Path, "at-live")
		if err == nil || !strings.Contains(err.Error(), "SERVERS_MCP_ENABLED") || !strings.Contains(err.Error(), "404") {
			t.Errorf("want the switched-off hint, got %v", err)
		}
	})
	t.Run("a refused token is a 401 with the code", func(t *testing.T) {
		s := newServerStub(t)
		ts := serverToolset("org-test-worker")
		_, err := probeServerTools(ts, s.srv.URL+ts.Path, "wrong")
		if err == nil || !strings.Contains(err.Error(), "401 INVALID_TOKEN") {
			t.Errorf("want 401 INVALID_TOKEN, got %v", err)
		}
	})
}

// TestServerCheck runs the whole chain against a stub that is both the
// /platform issuer and the per-server address. Like the platform check tests,
// it needs the file-based credential stores (see isolate).
func TestServerCheck(t *testing.T) {
	// One server: the platform stub's issuer + discovery, plus the per-server
	// address, so the discovery on /platform and the probe on the address
	// share an origin the way they do on a real MCP host.
	newCombined := func(t *testing.T) (*platformStub, *serverStub) {
		t.Helper()
		ps := newPlatformStub(t)
		ss := newServerStub(t)
		platformHandler, serverHandler := ps.srv.Config.Handler, ss.srv.Config.Handler
		combined := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/platform/servers/") {
				serverHandler.ServeHTTP(w, r)
				return
			}
			platformHandler.ServeHTTP(w, r)
		}))
		t.Cleanup(combined.Close)
		// The platform stub names its own URL in discovery documents; point
		// it at the combined origin so issuer and resource match.
		ps.srv.Close()
		ps.srv = combined
		return ps, ss
	}

	t.Run("happy path reports the tool count", func(t *testing.T) {
		isolate(t)
		ps, _ := newCombined(t)
		t.Setenv("DIBBLA_MCP_URL", ps.srv.URL)
		storeGrant(t, ps, false)
		var buf bytes.Buffer
		if err := runServerCheck(&buf, "org-test-worker"); err != nil {
			t.Fatalf("check failed: %v\n%s", err, buf.String())
		}
		for _, want := range []string{"/platform/servers/org-test-worker", "Discovery: ✅", "shared with /platform", "Token:     ✅ valid for", "Server:    ✅ dibbla-mcp 2.0.0", "Tools:     ✅ 2", "word_stats"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("output missing %q:\n%s", want, buf.String())
			}
		}
	})

	t.Run("empty server is a finding", func(t *testing.T) {
		isolate(t)
		ps, _ := newCombined(t)
		t.Setenv("DIBBLA_MCP_URL", ps.srv.URL)
		storeGrant(t, ps, false)
		var buf bytes.Buffer
		err := runServerCheck(&buf, "nobody")
		if err == nil || !strings.Contains(err.Error(), "offers no tools") || !strings.Contains(err.Error(), "dibbla functions exposed") {
			t.Errorf("want the empty-server finding, got %v", err)
		}
	})

	t.Run("flag off is 404 with the operator hint", func(t *testing.T) {
		isolate(t)
		ps, ss := newCombined(t)
		ss.enabled = false
		t.Setenv("DIBBLA_MCP_URL", ps.srv.URL)
		storeGrant(t, ps, false)
		err := runServerCheck(&bytes.Buffer{}, "org-test-worker")
		if err == nil || !strings.Contains(err.Error(), "SERVERS_MCP_ENABLED") {
			t.Errorf("want the switched-off hint, got %v", err)
		}
	})

	t.Run("missing grant says --login on this command", func(t *testing.T) {
		isolate(t)
		ps, _ := newCombined(t)
		t.Setenv("DIBBLA_MCP_URL", ps.srv.URL)
		err := runServerCheck(&bytes.Buffer{}, "org-test-worker")
		if err == nil || !strings.Contains(err.Error(), "dibbla mcp server org-test-worker --login") {
			t.Errorf("want a --login hint, got %v", err)
		}
	})
}
