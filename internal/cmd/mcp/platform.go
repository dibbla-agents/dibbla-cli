package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/auth"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/platformcontract"
	"github.com/dibbla-agents/dibbla-cli/internal/platformoauth"
)

var (
	platformClient string
	platformLogin  bool
	platformCheck  bool
	platformLogout bool
	platformUpload string
	platformScope  string
)

var platformCmd = &cobra.Command{
	Use:   "platform",
	Short: "Connect an MCP client to the Dibbla platform toolset (OAuth), log in, or check the connection",
	Args:  cobra.NoArgs,
	// A failed check or login is a diagnosis, not a usage mistake.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		switch {
		case platformLogout:
			return runPlatformLogout(w)
		case platformLogin:
			return runPlatformLogin(w)
		case platformCheck:
			return runPlatformCheck(w)
		case platformUpload != "":
			return runPlatformUpload(w, platformUpload)
		default:
			return runPlatform(w, platformClient)
		}
	},
}

func init() {
	platformCmd.Long = platformLong()
	platformCmd.Flags().StringVar(&platformClient, "client", "",
		"emit config for one client: claude, codex, or gemini (default: all three)")
	platformCmd.Flags().BoolVar(&platformLogin, "login", false,
		"authorize this machine against the platform toolset (browser flow) and store the grant")
	platformCmd.Flags().BoolVar(&platformCheck, "check", false,
		"verify the connection end to end: discovery, token status, initialize, and platform_whoami")
	platformCmd.Flags().BoolVar(&platformLogout, "logout", false,
		"revoke the stored grant server-side and forget it")
	platformCmd.Flags().StringVar(&platformUpload, "upload", "",
		"upload a file to the platform over the connector and print its file id")
	platformCmd.Flags().StringVar(&platformScope, "scope", "",
		"with --login: the scopes to ask consent for (default: the read scopes)")
	platformCmd.MarkFlagsMutuallyExclusive("login", "check", "logout", "upload")
}

// platformLong renders the help text. The capability boundary at the end —
// what is deliberately local-only, and why — is read from the vendored
// contract (DIB-532) rather than written here, so the help cannot describe a
// boundary the contract no longer draws.
func platformLong() string {
	var b strings.Builder
	b.WriteString(`Connects an MCP client to the platform toolset — your Dibbla applications,
deployments, logs and more over MCP at mcp.<domain>/platform, acting as you
in one organization.

Unlike the community toolset, /platform is protected by OAuth: the client
config carries NO token. Claude Code, Codex CLI and Gemini CLI discover the
authorization server themselves and open a browser for consent the first time
the server is used. Read scopes are granted at consent; writes need a wider
grant.

  dibbla mcp platform                    print config for all three clients
  dibbla mcp platform --client claude    print just one client's form
  dibbla mcp platform --login            authorize this machine (browser) and
                                         store the grant in the OS keyring
  dibbla mcp platform --check            prove the chain: discovery, token
                                         status, initialize, platform_whoami
  dibbla mcp platform --logout           revoke the grant and forget it
  dibbla mcp platform --upload FILE      prepare an upload intent and move the
                                         bytes from this process, resuming from
                                         the committed offset if the link drops
  dibbla mcp platform --login \
    --scope "..."                        ask consent for more than the read
                                         scopes; a write scope is never granted
                                         by default and has to be ticked on the
                                         consent page

--upload is what an MCP client with a filesystem does, run by hand: the file
bytes never enter a tool call, and the only credential involved is the access
token this machine already holds. It needs a grant with platform:files:write.
A client without a filesystem uses the same intent and hands its console link
to the human who has the file instead.

A write scope is never a consent default: --login without --scope stores a
read-only grant, and the consent page shows each write scope as a box you tick.

The grant --login stores belongs to the active context (see dibbla context)
and is never written to a config file. --check refreshes an expired access
token automatically; a revoked grant is reported as such with the fix.

THE SHAPE OF THE TOOLSET. A full write grant lists 30 tools for everything the
platform can do, so there is no tool per command. A tool is a flow, and the
step is a parameter: platform_apps lists your applications when you omit the
alias and reads one when you name it, platform_operation takes a view of
status/events/logs/output, platform_files and
platform_deployment_proposals take an action, and every irreversible change is
platform_destructive_plan with a resource, a human's approval in the console,
and then platform_destructive_execute. A grant sees only the tools its scopes
carry, and on a merged tool only the steps its scopes allow — an action you
cannot see is not missing, it is not yours.

`)
	local := platformcontract.CapabilitiesInState(platformcontract.StateLocalOnly)
	if len(local) > 0 {
		fmt.Fprintf(&b, "Deliberately NOT available over /platform (capability contract %s, local-only):\n",
			platformcontract.ContractVersion())
		for _, c := range local {
			fmt.Fprintf(&b, "  %-26s %s\n", c.ID, c.Title)
			if c.Reason != "" {
				fmt.Fprintf(&b, "  %-26s   %s\n", "", c.Reason)
			}
		}
		b.WriteString("These stay in this CLI. An agent that needs them runs `dibbla …` locally.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func runPlatform(w io.Writer, client string) error {
	endpoint, source, err := platformToolset.endpoint()
	if err != nil {
		return err
	}
	return platformToolset.printConfig(w, client, endpoint, source)
}

// --- grant storage -----------------------------------------------------------

// grantContextName is the context a platform grant belongs to: the active
// context, or — before any `dibbla login` — the name the API URL would derive,
// so the grant lands where a later login will look for it.
func grantContextName() string {
	if r := config.ResolveContext(); r.Err == nil && r.Name != "" {
		return r.Name
	}
	return contextcfg.DeriveName(config.Load().APIURL, config.DefaultAPIURL)
}

// grantStore names where a grant was read from, for the check output.
type grantStore string

const (
	grantStoreNone    grantStore = ""
	grantStoreKeyring grantStore = "the OS keyring"
	grantStoreFile    grantStore = "the credentials file"
)

func loadGrant(ctxName string) (*platformoauth.Grant, grantStore, error) {
	if raw, err := credential.GetPlatformGrant(ctxName); err == nil && len(raw) > 0 {
		g, err := platformoauth.Decode(raw)
		return g, grantStoreKeyring, err
	}
	raw, err := credential.GetPlatformGrantFile(ctxName)
	if err != nil {
		return nil, grantStoreNone, err
	}
	if len(raw) == 0 {
		return nil, grantStoreNone, nil
	}
	g, err := platformoauth.Decode(raw)
	return g, grantStoreFile, err
}

// saveGrant writes the grant back to the store it came from, or — for a new
// grant — to the keyring with the file fallback `dibbla login` uses.
func saveGrant(ctxName string, g *platformoauth.Grant, store grantStore) (grantStore, error) {
	raw, err := g.Encode()
	if err != nil {
		return store, err
	}
	if store == grantStoreFile {
		return store, credential.SetPlatformGrantFile(ctxName, raw)
	}
	if err := credential.SetPlatformGrant(ctxName, raw); err != nil {
		if !credential.IsKeyringUnavailable(err) {
			return store, fmt.Errorf("the grant could not be stored: %w", err)
		}
		if ferr := credential.SetPlatformGrantFile(ctxName, raw); ferr != nil {
			return store, fmt.Errorf("OS keyring unavailable on this host AND the file fallback failed: %w", ferr)
		}
		return grantStoreFile, nil
	}
	return grantStoreKeyring, nil
}

func forgetGrant(ctxName string) {
	_ = credential.DeletePlatformGrant(ctxName)
	_ = credential.DeletePlatformGrantFile(ctxName)
}

func platformClientID() string {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_PLATFORM_CLIENT_ID")); v != "" {
		return v
	}
	return platformoauth.DefaultClientID
}

// --- login -------------------------------------------------------------------

func runPlatformLogin(w io.Writer) error {
	if auth.IsSSHSession() {
		return errors.New("the browser flow cannot complete over SSH: the callback lands on this host's loopback, and the browser is on the other side of the connection. Run `dibbla mcp platform --login` on the machine with the browser")
	}
	endpoint, source, err := platformToolset.endpoint()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Endpoint:  %s  (%s)\n", endpoint, source.Source)

	pr, as, err := discoverPlatform(source.URL)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Discovery: %s issuer %s\n", platform.Icon("✅", "[OK]"), as.Issuer)

	ctxName := grantContextName()
	opts := platformoauth.AuthorizeOptions{
		ClientID: platformClientID(),
		Scope:    strings.TrimSpace(platformScope),
		OpenURL: func(u string) {
			if auth.HasGraphicalSession() {
				fmt.Fprintf(w, "%s Opening the browser for consent...\n", platform.Icon("🌐", "[>]"))
				if err := auth.OpenBrowser(u); err == nil {
					fmt.Fprintf(w, "  If the browser did not open, visit:\n  %s\n", u)
					return
				}
				if auth.CopyToClipboard(u) == nil {
					fmt.Fprintf(w, "  %s Authorization URL copied to clipboard.\n", platform.Icon("📋", "[*]"))
				}
			} else {
				fmt.Fprintf(w, "%s No graphical session detected. Open this URL in a browser on this machine:\n", platform.Icon("🌐", "[>]"))
			}
			fmt.Fprintf(w, "  %s\n", u)
		},
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Waiting for the browser to return... (press Ctrl+C to cancel)")
	g, err := platformoauth.Authorize(context.Background(), as, pr.Resource, opts)
	if err != nil {
		return loginAdvice(err, opts.ClientID)
	}

	store, err := saveGrant(ctxName, g, grantStoreNone)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s Connected. Grant for context %q stored in %s.\n", platform.Icon("✅", "[OK]"), ctxName, store)
	fmt.Fprintf(w, "Scopes: %s\n", strings.Join(g.Scopes(), ", "))
	fmt.Fprintf(w, "Run `dibbla mcp platform --check` to verify the chain end to end.\n")
	return nil
}

// loginAdvice attaches the fix to the failures a login is most likely to hit.
func loginAdvice(err error, clientID string) error {
	var oe *platformoauth.OAuthError
	if errors.As(err, &oe) {
		switch oe.Code {
		case "invalid_client":
			return fmt.Errorf("%w\nThe issuer does not know the client %q. Every install must pre-register the CLI's public client (see auth-service scripts/e2e/seed-dev-platform-client.sh); set DIBBLA_PLATFORM_CLIENT_ID if it was registered under another id", err, clientID)
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "invalid_client") || strings.Contains(msg, "unknown or unusable client_id") {
		return fmt.Errorf("%w\nThe issuer does not know the client %q — it must be pre-registered on this install; set DIBBLA_PLATFORM_CLIENT_ID if it was registered under another id", err, clientID)
	}
	if strings.Contains(msg, "redirect_uri") {
		return fmt.Errorf("%w\nThe registered callbacks for client %q must include http://127.0.0.1:<port>%s for each port in %v", err, clientID, platformoauth.CallbackPath, platformoauth.CallbackPorts)
	}
	return err
}

// discoverPlatform walks RFC 9728 → RFC 8414 from the MCP host and reports
// each failure in the terms the user acts on.
func discoverPlatform(mcpBase string) (*platformoauth.ProtectedResource, *platformoauth.AuthServer, error) {
	pr, err := platformoauth.DiscoverResource(mcpBase)
	if err != nil {
		if errors.Is(err, platformoauth.ErrToolsetNotEnabled) {
			return nil, nil, fmt.Errorf("%s — %s", platformToolset.NotFoundHint, err)
		}
		return nil, nil, fmt.Errorf("discovery failed on the MCP host: %w", err)
	}
	as, err := platformoauth.DiscoverAuthServer(pr.AuthorizationServers[0])
	if err != nil {
		return nil, nil, fmt.Errorf("discovery failed on the issuer named by %s: %w", platformoauth.ResourceMetadataURL(mcpBase), err)
	}
	return pr, as, nil
}

// --- logout ------------------------------------------------------------------

func runPlatformLogout(w io.Writer) error {
	ctxName := grantContextName()
	g, store, err := loadGrant(ctxName)
	if err != nil {
		// Unreadable is still forgettable.
		forgetGrant(ctxName)
		return fmt.Errorf("%w — the stored grant was discarded; the server-side grant, if any, expires on its own or can be disconnected from the console", err)
	}
	if g == nil {
		fmt.Fprintf(w, "No platform grant stored for context %q.\n", ctxName)
		return nil
	}
	revokeErr := platformoauth.Revoke(g)
	forgetGrant(ctxName)
	if revokeErr != nil {
		fmt.Fprintf(w, "%s Forgot the grant for context %q (from %s).\n", platform.Icon("✅", "[OK]"), ctxName, store)
		return fmt.Errorf("the server-side revocation did not complete: %w — the grant is forgotten locally and expires on its own; it can also be disconnected from the console", revokeErr)
	}
	fmt.Fprintf(w, "%s Revoked the grant for context %q and forgot it (was in %s).\n", platform.Icon("✅", "[OK]"), ctxName, store)
	return nil
}

// --- check -------------------------------------------------------------------

// runPlatformCheck proves the chain an OAuth MCP client walks, one line per
// link, and stops at the first broken one with the fix. Exit status is
// non-zero on any failure — a check that "mostly passes" is the state the
// command exists to make visible.
func runPlatformCheck(w io.Writer) error {
	endpoint, source, err := platformToolset.endpoint()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Endpoint:  %s  (%s)\n", endpoint, source.Source)

	pr, as, err := discoverPlatform(source.URL)
	if err != nil {
		fmt.Fprintf(w, "Discovery: %s\n", platform.Icon("❌", "[X]"))
		return err
	}
	fmt.Fprintf(w, "Discovery: %s issuer %s  (resource %s; %s)\n",
		platform.Icon("✅", "[OK]"), as.Issuer, pr.Resource, describeAuthServer(as))
	if pr.Resource != endpoint {
		fmt.Fprintf(w, "           %s the protected resource names %s, but this check targets %s — a token for one is refused by the other\n",
			platform.Icon("⚠️", "[!]"), pr.Resource, endpoint)
	}

	ctxName := grantContextName()
	g, store, err := loadGrant(ctxName)
	if err != nil {
		fmt.Fprintf(w, "Token:     %s\n", platform.Icon("❌", "[X]"))
		return fmt.Errorf("%w — run `dibbla mcp platform --login` to replace it", err)
	}
	if g == nil {
		fmt.Fprintf(w, "Token:     %s missing — no platform grant stored for context %q\n", platform.Icon("❌", "[X]"), ctxName)
		return fmt.Errorf("no platform grant for context %q: run `dibbla mcp platform --login`", ctxName)
	}
	if g.Issuer != as.Issuer || g.Resource != pr.Resource {
		fmt.Fprintf(w, "Token:     %s the stored grant is for %s at %s, not this install\n", platform.Icon("❌", "[X]"), g.Resource, g.Issuer)
		return fmt.Errorf("the stored grant does not belong to this install: run `dibbla mcp platform --login` here")
	}
	// Endpoints may have moved since login; use what discovery says now.
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

	probe, err := probeToolset(platformToolset, endpoint, g.AccessToken)
	if err != nil {
		fmt.Fprintf(w, "Server:    %s\n", platform.Icon("❌", "[X]"))
		return platformProbeAdvice(err)
	}
	fmt.Fprintf(w, "Server:    %s %s %s\n", platform.Icon("✅", "[OK]"), probe.ServerName, probe.ServerVersion)

	who, err := parseWhoami(probe.WhoamiStructured)
	if err != nil || who.UserID == "" {
		// The text form is still proof of identity; only the structured
		// breakdown is missing.
		fmt.Fprintf(w, "Identity:  %s\n\n%s\n", platform.Icon("✅", "[OK]"), strings.TrimSpace(probe.WhoamiText))
		return nil
	}
	fmt.Fprintf(w, "Identity:  %s %s\n", platform.Icon("✅", "[OK]"), who.person())
	fmt.Fprintf(w, "Org:       %s%s\n", who.orgLabel(), who.roleSuffix())
	if len(who.Scopes) > 0 {
		fmt.Fprintf(w, "Scopes:    %s\n", strings.Join(who.Scopes, ", "))
	} else {
		fmt.Fprintf(w, "Scopes:    (none)\n")
	}
	if who.Auth != "" {
		fmt.Fprintf(w, "Auth:      %s (client %s)\n", who.Auth, who.ClientID)
	}
	return nil
}

func describeAuthServer(as *platformoauth.AuthServer) string {
	parts := []string{"PKCE S256"}
	if as.SupportsRefresh() {
		parts = append(parts, "refresh")
	}
	if as.RevocationEndpoint != "" {
		parts = append(parts, "revoke")
	}
	if as.IntrospectionEndpoint != "" {
		parts = append(parts, "introspect")
	}
	return strings.Join(parts, ", ")
}

// tokenState is the outcome of deciding whether the stored access token can be
// presented, refreshing it when it cannot.
type tokenState struct {
	text      string
	refreshed bool
}

func (s tokenState) String() string { return s.text }

// tokenStatus answers "valid / expired → refreshed / revoked / dead" for a
// grant, in that order of checks:
//
//  1. An expired access token is refreshed. The refresh endpoint's
//     invalid_grant is the server saying the grant itself is gone — revoked,
//     expired, or its family killed by a replay — which is the "run --login"
//     answer, not a 401 from the MCP endpoint later.
//  2. A live-looking token is introspected when the issuer offers it, because
//     a JWT does not change when its grant is revoked: this is the only way
//     to report "revoked" before the endpoint refuses it.
func tokenStatus(g *platformoauth.Grant) (tokenState, error) {
	now := time.Now()
	if g.Expired(now) {
		if err := platformoauth.Refresh(g); err != nil {
			if platformoauth.IsInvalidGrant(err) {
				return tokenState{text: "expired, and the grant behind it is revoked or expired"},
					fmt.Errorf("the platform grant has been revoked or has expired (%v): run `dibbla mcp platform --login`", err)
			}
			return tokenState{text: "expired, and refresh failed"},
				fmt.Errorf("the access token has expired and could not be refreshed: %w", err)
		}
		return tokenState{text: fmt.Sprintf("expired → refreshed, valid for %s", untilText(g.ExpiresAt, now)), refreshed: true}, nil
	}

	in, err := platformoauth.Introspect(g)
	switch {
	case errors.Is(err, platformoauth.ErrNoIntrospection):
		return tokenState{text: fmt.Sprintf("valid for %s (not introspected: the issuer offers no introspection)", untilText(g.ExpiresAt, now))}, nil
	case err != nil:
		return tokenState{text: "could not be introspected"},
			fmt.Errorf("the issuer could not say whether the token is still honoured: %w", err)
	case !in.Active:
		return tokenState{text: "revoked — the issuer no longer honours this grant"},
			fmt.Errorf("the platform grant has been revoked (disconnected from the console, or replaced): run `dibbla mcp platform --login`")
	}
	if in.Exp > 0 {
		g.ExpiresAt = time.Unix(in.Exp, 0)
	}
	return tokenState{text: fmt.Sprintf("valid for %s", untilText(g.ExpiresAt, now))}, nil
}

func untilText(t, now time.Time) string {
	d := t.Sub(now).Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	return d.String()
}

// platformProbeAdvice maps the MCP endpoint's refusal codes onto the fix.
func platformProbeAdvice(err error) error {
	ee, ok := asEndpointError(err)
	if !ok {
		return err
	}
	switch {
	case ee.Status == 401:
		// The token was live a moment ago per the issuer; the endpoint
		// disagreeing means a verification-side problem, not a stale grant.
		return fmt.Errorf("%w\nThe issuer reported the grant live but the MCP endpoint refuses the token — the endpoint and the issuer disagree on issuer/resource/secret configuration. Run `dibbla mcp platform --login` once; if it persists, this install's PLATFORM_* configuration is inconsistent", err)
	case ee.Code == "INSUFFICIENT_SCOPE":
		return fmt.Errorf("%w\nThe grant lacks a scope this call needs. Run `dibbla mcp platform --logout` then `--login` to consent again", err)
	case ee.Code == "FORBIDDEN":
		return fmt.Errorf("%w\nThe grant's organization or user is no longer valid on the server. Run `dibbla mcp platform --login` to grant access again", err)
	case ee.Status == 404:
		return err
	case ee.Status == 503:
		return err
	}
	return err
}

// whoami is platform_whoami's structuredContent.
type whoami struct {
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Organization struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"organization"`
	Scopes   []string `json:"scopes"`
	ClientID string   `json:"client_id"`
	Auth     string   `json:"auth"`
}

func (w whoami) person() string {
	switch {
	case w.Name != "" && w.Email != "":
		return fmt.Sprintf("%s <%s>", w.Name, w.Email)
	case w.Name != "":
		return w.Name
	case w.Email != "":
		return w.Email
	}
	return w.UserID
}

func (w whoami) orgLabel() string {
	o := w.Organization
	switch {
	case o.Name != "" && o.Slug != "":
		return fmt.Sprintf("%s (%s)", o.Name, o.Slug)
	case o.Name != "":
		return o.Name
	case o.Slug != "":
		return o.Slug
	}
	return o.ID
}

func (w whoami) roleSuffix() string {
	if w.Organization.Role == "" {
		return ""
	}
	return " — role " + w.Organization.Role
}

func parseWhoami(raw json.RawMessage) (whoami, error) {
	var w whoami
	if len(raw) == 0 {
		return w, errors.New("no structuredContent")
	}
	err := json.Unmarshal(raw, &w)
	return w, err
}
