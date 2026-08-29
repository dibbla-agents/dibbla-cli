package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
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
	endpoint, source, err := communityToolset.endpoint()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Endpoint: %s  (%s)\n", endpoint, source.Source)

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

	probe, err := probeToolset(communityToolset, endpoint, cfg.APIToken)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Server:   ✅ %s %s\n", probe.ServerName, probe.ServerVersion)
	fmt.Fprintf(w, "Identity: ✅\n\n%s", probe.WhoamiText)

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

// probeResult is what one initialize + whoami round-trip proves.
type probeResult struct {
	ServerName    string
	ServerVersion string
	WhoamiText    string
	// WhoamiStructured is the tool's structuredContent when it returns one
	// (platform_whoami does; community_whoami does not).
	WhoamiStructured json.RawMessage
}

// probeToolset runs the MCP handshake and the toolset's whoami tool with the
// given bearer, the shared half of every --check.
func probeToolset(ts toolset, endpoint, token string) (*probeResult, error) {
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
	if err := rpcCall(client, ts, endpoint, token, "initialize", init, &initResp); err != nil {
		return nil, err
	}
	out := &probeResult{ServerName: initResp.Result.ServerInfo.Name, ServerVersion: initResp.Result.ServerInfo.Version}

	var whoResp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	call := map[string]any{"name": ts.WhoamiTool, "arguments": map[string]any{}}
	if err := rpcCall(client, ts, endpoint, token, "tools/call", call, &whoResp); err != nil {
		return nil, err
	}
	if whoResp.Error != nil {
		return nil, fmt.Errorf("%s failed: %s (JSON-RPC %d)", ts.WhoamiTool, whoResp.Error.Message, whoResp.Error.Code)
	}
	if len(whoResp.Result.Content) > 0 {
		out.WhoamiText = whoResp.Result.Content[0].Text
	}
	if whoResp.Result.IsError {
		return nil, fmt.Errorf("%s failed: %s", ts.WhoamiTool, out.WhoamiText)
	}
	out.WhoamiStructured = whoResp.Result.StructuredContent
	return out, nil
}

// endpointError is an HTTP-level refusal from the MCP endpoint, kept typed so
// the platform check can map the server's error code (INVALID_TOKEN,
// INSUFFICIENT_SCOPE, …) onto the right advice.
type endpointError struct {
	Status  int
	Code    string
	Message string
	Body    string
	WWWAuth string
}

func (e *endpointError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// rpcCall posts one JSON-RPC request and decodes the response, unwrapping SSE
// framing when the server answers as an event stream. HTTP-level failures are
// translated into the errors a user can act on; the platform check unwraps
// the *endpointError beneath them for finer advice.
func rpcCall(client *http.Client, ts toolset, endpoint, token, method string, params, out any) error {
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
	default:
		ee := &endpointError{Status: resp.StatusCode, Body: string(raw), WWWAuth: resp.Header.Get("WWW-Authenticate")}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			ee.Code, ee.Message = env.Error.Code, env.Error.Message
		}
		return describeEndpointError(ts, endpoint, ee)
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

// describeEndpointError wraps an endpointError in the sentence a user acts on.
// The wrapped error stays reachable through errors.As.
func describeEndpointError(ts toolset, endpoint string, ee *endpointError) error {
	switch ee.Status {
	case http.StatusUnauthorized:
		if ts.StaticBearer {
			return fmt.Errorf("the endpoint rejected the token (HTTP 401) — the token is invalid or expired; run `dibbla login` and retry%w", hidden(ee))
		}
		return fmt.Errorf("the endpoint rejected the token (HTTP 401 %s): %s%w", ee.Code, ee.Message, hidden(ee))
	case http.StatusForbidden:
		if ee.Code != "" {
			return fmt.Errorf("the endpoint refused the request (HTTP 403 %s): %s%w", ee.Code, ee.Message, hidden(ee))
		}
		return fmt.Errorf("the endpoint refused the request (HTTP 403): %s%w", strings.TrimSpace(ee.Body), hidden(ee))
	case http.StatusNotFound:
		return fmt.Errorf("%s%w", ts.NotFoundHint, hidden(ee))
	case http.StatusServiceUnavailable:
		return fmt.Errorf("the endpoint cannot verify tokens right now (HTTP 503 %s): %s — the platform's auth service is unreachable; retry shortly%w", ee.Code, ee.Message, hidden(ee))
	default:
		return fmt.Errorf("unexpected HTTP %d from %s: %s%w", ee.Status, endpoint, strings.TrimSpace(ee.Body), hidden(ee))
	}
}

// hidden wraps an error so it is reachable with errors.As but adds nothing to
// the message — the sentence around it already says everything.
func hidden(err error) error { return silent{err} }

type silent struct{ err error }

func (s silent) Error() string { return "" }
func (s silent) Unwrap() error { return s.err }

// asEndpointError returns the *endpointError behind err, if any.
func asEndpointError(err error) (*endpointError, bool) {
	var ee *endpointError
	if errors.As(err, &ee) {
		return ee, true
	}
	return nil, false
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
