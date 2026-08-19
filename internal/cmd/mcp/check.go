package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// runCommunityCheck verifies the whole connection chain end to end, the way an
// MCP client would meet it: resolve the endpoint, resolve the token exactly the
// way the CLI does (env → keyring → credentials file), then initialize and call
// community_whoami. The configuration has in practice a single failure point —
// the token reaching the client's environment — and without this command that
// failure is invisible until some other tool call fails (review 2026-08-19,
// finding A3).
func runCommunityCheck(w io.Writer) error {
	r := resolveMCPURL()
	if r.URL == "" {
		return fmt.Errorf("cannot resolve the MCP host: %s\nSet DIBBLA_MCP_URL explicitly, e.g. DIBBLA_MCP_URL=https://mcp.dibbla.com", r.Source)
	}
	endpoint := r.URL + "/community"
	fmt.Fprintf(w, "Endpoint: %s  (%s)\n", endpoint, r.Source)

	// The source matters as much as the presence: every client config this
	// command emits references ${DIBBLA_API_TOKEN}, so a token that resolves
	// only via the keyring or the credentials file means the check can pass
	// while an MCP client started from this same shell gets 401 — exactly the
	// failure the 2026-08-19 review's finding A1 hit. Read the env var here
	// (the CLI has already folded any ./.env into the environment at dispatch,
	// which counts: a client started from this directory-agnostic shell still
	// needs the export).
	envTok := strings.TrimSpace(os.Getenv("DIBBLA_API_TOKEN"))
	cfg := config.Load()
	if cfg.APIToken == "" {
		return fmt.Errorf("no Dibbla token found (tried DIBBLA_API_TOKEN, the OS keyring, and the credentials file) — run `dibbla login`, or export DIBBLA_API_TOKEN")
	}
	fmt.Fprintf(w, "Token:    present (%d chars, from %s)\n", len(cfg.APIToken), tokenSource(envTok, cfg.APIToken))

	client := &http.Client{Timeout: 15 * time.Second}

	init := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "dibbla-cli", "version": "check"},
	}
	var initResp struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := rpcCall(client, endpoint, cfg.APIToken, "initialize", init, &initResp); err != nil {
		return err
	}
	fmt.Fprintf(w, "Server:   ✅ %s %s\n", initResp.Result.ServerInfo.Name, initResp.Result.ServerInfo.Version)

	var whoResp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	call := map[string]any{"name": "community_whoami", "arguments": map[string]any{}}
	if err := rpcCall(client, endpoint, cfg.APIToken, "tools/call", call, &whoResp); err != nil {
		return err
	}
	var text string
	if len(whoResp.Result.Content) > 0 {
		text = whoResp.Result.Content[0].Text
	}
	if whoResp.Result.IsError {
		return fmt.Errorf("community_whoami failed: %s", text)
	}
	fmt.Fprintf(w, "Identity: ✅\n\n%s", text)

	if envTok == "" {
		fmt.Fprintf(w, "\n⚠️  The server chain works, but DIBBLA_API_TOKEN is NOT exported in this shell.\n")
		return fmt.Errorf("every client config `dibbla mcp community` emits references ${DIBBLA_API_TOKEN}, so an MCP client started from this environment gets 401 — export the token where the client starts (see the keychain snippet in your shell init), then re-run --check")
	}
	return nil
}

// tokenSource names where the resolved token came from, mirroring
// config.Load's precedence (env → OS keyring → credentials file).
func tokenSource(envTok, resolved string) string {
	if envTok != "" {
		return "the DIBBLA_API_TOKEN environment variable"
	}
	if kt, _, err := credential.GetCredentials(); err == nil && kt != "" && kt == resolved {
		return "the OS keyring"
	}
	return "the credentials file"
}

// rpcCall posts one JSON-RPC request and decodes the response, unwrapping SSE
// framing when the server answers as an event stream. HTTP-level failures are
// translated into the errors a user can act on.
func rpcCall(client *http.Client, endpoint, token, method string, params, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
	case http.StatusUnauthorized:
		return fmt.Errorf("the endpoint rejected the token (HTTP 401) — the token is invalid or expired; run `dibbla login` and retry")
	case http.StatusForbidden:
		return fmt.Errorf("the endpoint refused the request (HTTP 403): %s", strings.TrimSpace(string(raw)))
	case http.StatusNotFound:
		return fmt.Errorf("no community toolset at this endpoint (HTTP 404) — the community is a central-install feature; customer and dev installs serve 404 here by design")
	default:
		return fmt.Errorf("unexpected HTTP %d from %s: %s", resp.StatusCode, endpoint, strings.TrimSpace(string(raw)))
	}

	payload := extractJSON(string(raw))
	if payload == "" {
		return fmt.Errorf("no JSON-RPC payload in the response")
	}
	if err := json.Unmarshal([]byte(payload), out); err != nil {
		return fmt.Errorf("cannot decode the response: %w", err)
	}
	return nil
}

// extractJSON returns the first JSON document from either a plain JSON body or
// an SSE-framed one (`data: {...}` lines).
func extractJSON(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
		if strings.HasPrefix(line, "{") {
			return line
		}
	}
	return ""
}
