package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDeriveFromAPIURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://api.dibbla.com", want: "https://mcp.dibbla.com"},
		{in: "api.dibbla.com", want: "https://mcp.dibbla.com"},
		{in: "https://api.example.io:8443/v1/", want: "https://mcp.example.io:8443"},
		{in: "http://localhost:8090", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := deriveFromAPIURL(c.in)
		if c.wantErr != (err != nil) {
			t.Errorf("deriveFromAPIURL(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("deriveFromAPIURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// render emits the config for one client against a fixed endpoint.
func render(t *testing.T, client string) string {
	t.Helper()
	t.Setenv("DIBBLA_MCP_URL", "https://mcp.dibbla.com")
	var buf bytes.Buffer
	if err := runCommunity(&buf, client); err != nil {
		t.Fatalf("runCommunity(%q): %v", client, err)
	}
	return buf.String()
}

// Golden-shape assertions per P-0023 Part F: output parses, references the
// env var rather than a literal token, and never names an internal host.
func TestCommunityOutputs(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		out := render(t, "claude")
		jsonPart := out[strings.Index(out, "\n{")+1:]
		var v struct {
			MCPServers map[string]struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(jsonPart), &v); err != nil {
			t.Fatalf("claude output is not valid JSON: %v\n%s", err, jsonPart)
		}
		s := v.MCPServers["dibbla-community"]
		if s.URL != "https://mcp.dibbla.com/community" || s.Type != "http" {
			t.Errorf("unexpected server entry: %+v", s)
		}
		if !strings.Contains(s.Headers["Authorization"], "${DIBBLA_API_TOKEN}") {
			t.Errorf("claude headers must reference the env var: %+v", s.Headers)
		}
	})

	t.Run("codex", func(t *testing.T) {
		out := render(t, "codex")
		for _, want := range []string{
			"[mcp_servers.dibbla-community]",
			`url = "https://mcp.dibbla.com/community"`,
			`bearer_token_env_var = "DIBBLA_API_TOKEN"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("codex output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("gemini", func(t *testing.T) {
		out := render(t, "gemini")
		var v struct {
			MCPServers map[string]struct {
				HTTPURL string            `json:"httpUrl"`
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("gemini output is not valid JSON: %v\n%s", err, out)
		}
		if v.MCPServers["dibbla-community"].HTTPURL != "https://mcp.dibbla.com/community" {
			t.Errorf("unexpected gemini entry: %+v", v.MCPServers)
		}
	})

	t.Run("all clients, no secrets, no internal hosts", func(t *testing.T) {
		for _, client := range []string{"", "claude", "codex", "gemini"} {
			out := render(t, client)
			if strings.Contains(out, "ak_") {
				t.Errorf("client %q output contains a literal token", client)
			}
			if strings.Contains(out, "dibbla.net") {
				t.Errorf("client %q output names an internal host", client)
			}
		}
	})

	t.Run("unknown client errors", func(t *testing.T) {
		t.Setenv("DIBBLA_MCP_URL", "https://mcp.dibbla.com")
		var buf bytes.Buffer
		if err := runCommunity(&buf, "cursor"); err == nil {
			t.Error("unknown client should error")
		}
	})
}
