package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// `dibbla mcp server <tool-server-name>` (DIB-1069, part of DIB-1064).
//
// One tool server's exposed functions as separate MCP tools, at
// mcp.<domain>/platform/servers/<name>. The address lives on the same OAuth
// resource as /platform, so discovery, the grant and the login are the
// platform ones; only the endpoint, the client forms and the check differ.
//
// STRICTLY ADDITIVE. Nothing in here changes `dibbla mcp platform` or
// `dibbla mcp community`: the client forms this command prints (including the
// two extra clients, cursor and opencode) are its own, and the shared toolset
// printer keeps its three clients and its error for anything else.

var (
	serverClient string
	serverLogin  bool
	serverCheck  bool
)

// serverNameRe mirrors serversNameRe in app-hosting-service/mcp-server
// (servers_toolset.go): the only names the MCP host serves. Any other {name}
// is silently served the empty server, so the CLI refuses it here with the
// reason instead of printing a config that connects to nothing.
var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

const serverNameRule = "letters, digits, '.', '_' and '-', at most 64 characters"

// findNamesHint is how a person finds a tool server's name: the MCP address
// itself never reveals which servers exist (unknown, disconnected and
// nothing-exposed all look the same), so the CLI is where names come from.
const findNamesHint = "Find tool server names with `dibbla functions exposed` (the SERVER column)."

var serverCmd = &cobra.Command{
	Use:   "server <tool-server-name>",
	Short: "Print MCP client config for one tool server's exposed functions, as separate tools",
	Long: `Prints ready-to-paste configuration connecting an MCP client to ONE of your
organization's tool servers, at mcp.<domain>/platform/servers/<name>. Each of
that server's exposed functions appears as its own MCP tool, so the client can
switch them on and off one by one. Only exposed functions appear ('dibbla fn
expose'); the rest of the platform is not on this address.

This command only prints client configuration (and, with --login or --check,
logs in or verifies the connection). It never starts or runs a server: the
server is your worker, already registered with the platform.

  dibbla mcp platform            all exposed functions through one tool
                                 (platform_tools, the official connector)
  dibbla mcp server <name>       one tool server's exposed functions as
                                 separate tools

The address is protected by the same OAuth resource as /platform, so it
shares its login and its stored grant: a machine authorized with
'dibbla mcp platform --login' needs nothing more, and --login here does the
same thing.

  dibbla mcp server NAME                     print config for every client
  dibbla mcp server NAME --client cursor     print just one client's form
                                             (claude, codex, gemini, cursor,
                                             opencode)
  dibbla mcp server NAME --login             authorize this machine (browser)
  dibbla mcp server NAME --check             prove the chain: discovery, token
                                             status, initialize, tools/list

CONNECT BEFORE YOU LOG IN. On a per-server address some clients (Claude Code
among them) must connect once before their own login command works: the first
connection is what tells them where the authorization server is. The printed
steps are in that order — add the server, start a session or list servers,
then log in if asked. Do not run the client's login command first.

` + findNamesHint,
	// A failed check or login is a diagnosis, not a usage mistake.
	SilenceUsage: true,
	Args: func(cmd *cobra.Command, args []string) error {
		switch len(args) {
		case 0:
			return errors.New("a tool server name is required: dibbla mcp server <tool-server-name>\n" + findNamesHint)
		case 1:
			return nil
		default:
			return fmt.Errorf("one tool server name expected, got %d — quote a name that contains spaces, e.g. \"acme tools\" (although such a name cannot be an MCP address; see --help)", len(args))
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		name := args[0]
		if err := validateServerName(name); err != nil {
			return err
		}
		switch {
		case serverLogin:
			return runServerLogin(w, name)
		case serverCheck:
			return runServerCheck(w, name)
		default:
			return runServer(w, name, serverClient)
		}
	},
}

func init() {
	serverCmd.Flags().StringVar(&serverClient, "client", "",
		"emit config for one client: claude, codex, gemini, cursor, or opencode (default: all)")
	serverCmd.Flags().BoolVar(&serverLogin, "login", false,
		"authorize this machine against the platform resource (browser flow) and store the grant — the same login as `dibbla mcp platform --login`")
	serverCmd.Flags().BoolVar(&serverCheck, "check", false,
		"verify the connection end to end: discovery, token status, initialize, and tools/list on this server's address")
	serverCmd.MarkFlagsMutuallyExclusive("login", "check")
}

// serverToolset is the per-server address as a toolset value: the shared
// endpoint resolution, RPC and error mapping work unchanged on it. WhoamiTool
// stays empty on purpose — a server address has no whoami; the check lists
// tools instead.
func serverToolset(name string) toolset {
	return toolset{
		Path:         "/platform/servers/" + name,
		ServerName:   "dibbla-" + name,
		StaticBearer: false,
		NotFoundHint: "no per-server MCP address at this endpoint (HTTP 404) — /platform/servers/<name> is switched off on this install (SERVERS_MCP_ENABLED); ask the operator, or check DIBBLA_MCP_URL points at the right host",
	}
}

// exposedServerNames lists the tool servers that currently have an exposure
// in the caller's organization, from the same listing `dibbla functions
// exposed` prints. It is a variable so tests can answer without a server.
// Any failure (no token, no network, no permission) returns an error, which
// the caller treats as "cannot tell" — never as "not exposed".
var exposedServerNames = func() ([]string, error) {
	cfg := config.Load()
	if !cfg.HasToken() {
		return nil, errors.New("no API token")
	}
	resp, err := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false).Get("/api/wf/slim/tool-exposures?format=json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Exposures []struct {
			Server string `json:"server"`
		} `json:"exposures"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, e := range doc.Exposures {
		if e.Server != "" && !seen[e.Server] {
			seen[e.Server] = true
			names = append(names, e.Server)
		}
	}
	sort.Strings(names)
	return names, nil
}

// validateServerName applies the MCP host's rule, and tells the two failures
// apart. A name that the organization really has, but that the host cannot
// serve, is not a typo: it is a tool server that cannot be an MCP address
// until the worker renames it. The sdk-go side (WithServerName) accepts any
// string, so nothing upstream prevents it.
//
// There is deliberately no "use dibbla mcp platform instead" here: a name with
// a character that needs URL escaping cannot be exposed or invoked anywhere
// today, platform_tools included (DIB-1075).
func validateServerName(name string) error {
	if serverNameRe.MatchString(name) {
		return nil
	}
	if names, err := exposedServerNames(); err == nil {
		for _, n := range names {
			if n == name {
				return fmt.Errorf("the tool server %q exists, but its name cannot be used as an MCP address: an address allows %s. Rename the server in the worker (WithServerName in sdk-go), re-register it, and expose its functions again", name, serverNameRule)
			}
		}
	}
	return fmt.Errorf("invalid tool server name %q: an MCP address allows %s.\n%s", name, serverNameRule, findNamesHint)
}

// --- config ------------------------------------------------------------------

func runServer(w io.Writer, name, client string) error {
	ts := serverToolset(name)
	endpoint, source, err := ts.endpoint()
	if err != nil {
		return err
	}
	return printServerConfig(w, ts, client, endpoint, source)
}

// printServerConfig is this command's own client printer. It is not the
// shared toolset.printConfig on purpose: that one serves the existing
// commands and keeps its three clients; the two extra clients here are
// offered by `mcp server` only (DIB-1064 decision, 2026-09-24).
func printServerConfig(w io.Writer, ts toolset, client, endpoint string, source resolveResult) error {
	switch strings.ToLower(client) {
	case "claude":
		printServerClaude(w, ts, endpoint)
	case "codex":
		printServerCodex(w, ts, endpoint)
	case "gemini":
		printServerGemini(w, ts, endpoint)
	case "cursor":
		printServerCursor(w, ts, endpoint)
	case "opencode":
		printServerOpencode(w, ts, endpoint)
	case "":
		fmt.Fprintf(w, "# MCP endpoint: %s (%s)\n", endpoint, source.Source)
		fmt.Fprintf(w, "# One tool per exposed function of the tool server %q. No token in any form:\n", strings.TrimPrefix(ts.ServerName, "dibbla-"))
		fmt.Fprintf(w, "# the client runs the OAuth flow itself, on the same grant as /platform.\n")
		fmt.Fprintf(w, "# Connect first, log in second (see `dibbla mcp server --help`). Run\n")
		fmt.Fprintf(w, "# `dibbla mcp server %s --check` to verify the chain from this machine.\n\n", strings.TrimPrefix(ts.ServerName, "dibbla-"))
		fmt.Fprintf(w, "## Claude Code\n\n")
		printServerClaude(w, ts, endpoint)
		fmt.Fprintf(w, "\n## Codex CLI (~/.codex/config.toml)\n\n")
		printServerCodex(w, ts, endpoint)
		fmt.Fprintf(w, "\n## Gemini CLI (~/.gemini/settings.json)\n\n")
		printServerGemini(w, ts, endpoint)
		fmt.Fprintf(w, "\n## Cursor (~/.cursor/mcp.json or .cursor/mcp.json)\n\n")
		printServerCursor(w, ts, endpoint)
		fmt.Fprintf(w, "\n## opencode (opencode.json)\n\n")
		printServerOpencode(w, ts, endpoint)
	default:
		return fmt.Errorf("unknown --client %q (expected claude, codex, gemini, cursor, or opencode)", client)
	}
	return nil
}

// printServerClaude prints the Claude Code form in connect-before-login order.
// Run before any connection, `claude mcp login` on a per-server address tries
// path-suffixed discovery, gets a 404, falls back to treating the MCP host as
// the issuer, fails, and caches the failure (Claude Code 2.1.276, DIB-1068
// E2E). After one connection it follows the 401 to the /platform metadata and
// logs in fine — so the steps say "add, connect, then log in if asked".
func printServerClaude(w io.Writer, ts toolset, endpoint string) {
	fmt.Fprintf(w, `# 1. Add:
#      claude mcp add --transport http %s %s
# 2. Connect once BEFORE any login: start a Claude Code session, or run
#      claude mcp list
# 3. If it shows the server as needing authentication: in a session, /mcp →
#    %s → Authenticate (or now, and only now, `+"`claude mcp login %s`"+`).
# Or as .mcp.json / settings JSON — oauth.scopes asks for the writes a coding
# agent needs; the consent page still shows each one as a box to tick:
{
  "mcpServers": {
    "%s": {
      "type": "http",
      "url": "%s",
      "oauth": { "scopes": "%s" }
    }
  }
}
`, ts.ServerName, endpoint, ts.ServerName, ts.ServerName, ts.ServerName, endpoint, agentScopes())
}

func printServerCodex(w io.Writer, ts toolset, endpoint string) {
	fmt.Fprintf(w, `[mcp_servers.%s]
url = "%s"
# OAuth: no bearer_token_env_var. Start Codex once so it connects, then, if it
# asks for authentication:
#   codex mcp login %s
`, ts.ServerName, endpoint, ts.ServerName)
}

func printServerGemini(w io.Writer, ts toolset, endpoint string) {
	fmt.Fprintf(w, `{
  "mcpServers": {
    "%s": {
      "httpUrl": "%s",
      "oauth": { "enabled": true }
    }
  }
}
`, ts.ServerName, endpoint)
}

// printServerCursor: Cursor reads mcpServers from ~/.cursor/mcp.json (global)
// or .cursor/mcp.json (project) and runs the OAuth flow itself when the server
// is first used; there is no login command to run first.
func printServerCursor(w io.Writer, ts toolset, endpoint string) {
	fmt.Fprintf(w, `{
  "mcpServers": {
    "%s": {
      "url": "%s"
    }
  }
}
`, ts.ServerName, endpoint)
	fmt.Fprintf(w, "# No token: Cursor opens the browser for consent when the server is first\n")
	fmt.Fprintf(w, "# used (Settings → MCP shows the connection). Each tool can be switched on\n")
	fmt.Fprintf(w, "# and off there.\n")
}

// printServerOpencode: opencode.json, type "remote"; opencode runs OAuth
// itself on the first connection, `opencode mcp auth <name>` re-authorizes.
func printServerOpencode(w io.Writer, ts toolset, endpoint string) {
	fmt.Fprintf(w, `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "%s": {
      "type": "remote",
      "url": "%s",
      "enabled": true
    }
  }
}
`, ts.ServerName, endpoint)
	fmt.Fprintf(w, "# No token: opencode runs the OAuth flow itself when it first connects. Start\n")
	fmt.Fprintf(w, "# opencode once; if it then asks for authentication, run\n")
	fmt.Fprintf(w, "#   opencode mcp auth %s\n", ts.ServerName)
}

// --- login -------------------------------------------------------------------

// runServerLogin is the platform login: same resource, same grant, same
// storage key. Only the closing hint differs.
func runServerLogin(w io.Writer, name string) error {
	if err := runPlatformLogin(w); err != nil {
		return err
	}
	fmt.Fprintf(w, "The same grant serves %s: run `dibbla mcp server %s --check`.\n", serverToolset(name).Path, name)
	return nil
}

// --- check -------------------------------------------------------------------

// runServerCheck walks the chain an OAuth MCP client walks on a per-server
// address: /platform discovery (the address has no metadata of its own), the
// stored platform grant, then initialize and tools/list on the address. It
// ends with the tool count, because that is the one thing this address adds
// over /platform — and zero is a finding, not a pass.
func runServerCheck(w io.Writer, name string) error {
	ts := serverToolset(name)
	endpoint, source, err := ts.endpoint()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Endpoint:  %s  (%s)\n", endpoint, source.Source)

	pr, as, err := discoverPlatform(source.URL)
	if err != nil {
		fmt.Fprintf(w, "Discovery: %s\n", platform.Icon("❌", "[X]"))
		return err
	}
	fmt.Fprintf(w, "Discovery: %s issuer %s  (resource %s, shared with /platform; %s)\n",
		platform.Icon("✅", "[OK]"), as.Issuer, pr.Resource, describeAuthServer(as))

	ctxName := grantContextName()
	g, store, err := loadGrant(ctxName)
	if err != nil {
		fmt.Fprintf(w, "Token:     %s\n", platform.Icon("❌", "[X]"))
		return fmt.Errorf("%w — run `dibbla mcp server %s --login` to replace it", err, name)
	}
	if g == nil {
		fmt.Fprintf(w, "Token:     %s missing — no platform grant stored for context %q\n", platform.Icon("❌", "[X]"), ctxName)
		return fmt.Errorf("no platform grant for context %q: run `dibbla mcp server %s --login` (or `dibbla mcp platform --login`; it is the same grant)", ctxName, name)
	}
	if g.Issuer != as.Issuer || g.Resource != pr.Resource {
		fmt.Fprintf(w, "Token:     %s the stored grant is for %s at %s, not this install\n", platform.Icon("❌", "[X]"), g.Resource, g.Issuer)
		return fmt.Errorf("the stored grant does not belong to this install: run `dibbla mcp server %s --login` here", name)
	}
	g.TokenEndpoint, g.RevocationEndpoint, g.IntrospectionEndpoint = as.TokenEndpoint, as.RevocationEndpoint, as.IntrospectionEndpoint

	status, err := tokenStatus(g)
	if err != nil {
		fmt.Fprintf(w, "Token:     %s %s\n", platform.Icon("❌", "[X]"), status)
		return err
	}
	if status.refreshed {
		if store, err = saveGrant(ctxName, g, store); err != nil {
			fmt.Fprintf(w, "Token:     %s refreshed, but the new grant could not be stored\n", platform.Icon("❌", "[X]"))
			return err
		}
	}
	fmt.Fprintf(w, "Token:     %s %s  (context %q, from %s)\n", platform.Icon("✅", "[OK]"), status, ctxName, store)

	probe, err := probeServerTools(ts, endpoint, g.AccessToken)
	if err != nil {
		fmt.Fprintf(w, "Server:    %s\n", platform.Icon("❌", "[X]"))
		return platformProbeAdvice(err)
	}
	fmt.Fprintf(w, "Server:    %s %s %s\n", platform.Icon("✅", "[OK]"), probe.ServerName, probe.ServerVersion)
	if len(probe.Tools) == 0 {
		fmt.Fprintf(w, "Tools:     %s 0\n", platform.Icon("⚠️", "[!]"))
		return fmt.Errorf("the address answers but offers no tools: no tool server named %q is registered right now, or none of its functions is exposed (the address cannot tell the two apart). %s", name, findNamesHint)
	}
	fmt.Fprintf(w, "Tools:     %s %d\n", platform.Icon("✅", "[OK]"), len(probe.Tools))
	for _, t := range probe.Tools {
		fmt.Fprintf(w, "           %s\n", t)
	}
	return nil
}

// serverProbeResult is what initialize + tools/list prove on a server address.
type serverProbeResult struct {
	ServerName    string
	ServerVersion string
	Tools         []string
}

// probeServerTools runs the MCP handshake and lists the tools with the given
// bearer. It is the per-server counterpart of probeToolset, which calls a
// whoami tool no server address has.
func probeServerTools(ts toolset, endpoint, token string) (*serverProbeResult, error) {
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
	out := &serverProbeResult{ServerName: initResp.Result.ServerInfo.Name, ServerVersion: initResp.Result.ServerInfo.Version}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := rpcCall(client, ts, endpoint, token, "tools/list", map[string]any{}, &listResp); err != nil {
		return nil, err
	}
	if listResp.Error != nil {
		return nil, fmt.Errorf("tools/list failed: %s (JSON-RPC %d)", listResp.Error.Message, listResp.Error.Code)
	}
	for _, t := range listResp.Result.Tools {
		out.Tools = append(out.Tools, t.Name)
	}
	sort.Strings(out.Tools)
	return out, nil
}
