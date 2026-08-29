// Package platformoauth is the CLI's OAuth 2.1 public client for the Dibbla
// platform connector (P-0035 Part A as a client; DIB-544).
//
// It walks the same road any MCP client walks to reach mcp.<domain>/platform:
// RFC 9728 protected-resource metadata on the MCP host names the authorization
// server; RFC 8414 metadata on that server names the endpoints; PKCE S256 code
// flow with an RFC 8707 resource through a loopback callback; RFC 7009 / 7662
// for revocation and introspection; refresh-token rotation from DIB-529.
//
// Nothing here is Dibbla-shaped beyond the resource path. What is deliberate:
//
//   - The CLI is a PUBLIC client: no secret, client_id in the form body, and
//     PKCE is what binds the code to this process. The server refuses "plain"
//     and so does this package by never offering it.
//   - The loopback callback binds to a small FIXED set of ports. The server
//     matches redirect_uri exactly against the client's registration (no port
//     wildcards, RFC 8252 §7.3 notwithstanding — a registered list is a list),
//     so a random port would never match. The registration for the dibbla-cli
//     client carries exactly these ports.
//   - The resource parameter is sent on both legs and compared exactly by the
//     server; a token is minted for one /platform URL and nothing else.
package platformoauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultClientID is the pre-registered public client the CLI authorizes as.
// Overridable through DIBBLA_PLATFORM_CLIENT_ID for installs that registered
// the CLI under another id.
const DefaultClientID = "dibbla-cli"

// CallbackPorts is the fixed loopback port set the dibbla-cli client is
// registered with. The first one that binds is used; the redirect_uri sent to
// the server names that port, and the registration lists all three so any of
// them matches exactly.
var CallbackPorts = []int{48765, 48766, 48767}

// CallbackPath is the path component of every registered loopback callback.
const CallbackPath = "/callback"

// HTTPClient is the client every request in this package goes through; tests
// swap it for one pointed at a stub.
var HTTPClient = &http.Client{Timeout: 15 * time.Second}

// ProtectedResource is the RFC 9728 document served by the MCP host at
// /.well-known/oauth-protected-resource/platform.
type ProtectedResource struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	BearerMethods        []string `json:"bearer_methods_supported"`
}

// AuthServer is the RFC 8414 document served by the issuer at
// /.well-known/oauth-authorization-server. Only the members the client acts on.
type AuthServer struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RevocationEndpoint            string   `json:"revocation_endpoint"`
	IntrospectionEndpoint         string   `json:"introspection_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

// SupportsRefresh reports whether the server advertises the refresh grant.
func (a AuthServer) SupportsRefresh() bool { return contains(a.GrantTypesSupported, "refresh_token") }

// SupportsS256 reports whether the server accepts PKCE S256 — the only method
// this client will ever send.
func (a AuthServer) SupportsS256() bool { return contains(a.CodeChallengeMethodsSupported, "S256") }

// ErrToolsetNotEnabled is returned when the MCP host answers 404 for the
// protected-resource document: the /platform toolset is not switched on at
// this install (PLATFORM_RESOURCE_METADATA_ENABLED / PLATFORM_MCP_ENABLED off).
var ErrToolsetNotEnabled = errors.New("the platform toolset is not enabled on this install")

// ResourceMetadataURL is where the RFC 9728 document for /platform lives on an
// MCP host.
func ResourceMetadataURL(mcpBaseURL string) string {
	return strings.TrimRight(mcpBaseURL, "/") + "/.well-known/oauth-protected-resource/platform"
}

// AuthServerMetadataURL is where the RFC 8414 document lives on an issuer.
func AuthServerMetadataURL(issuer string) string {
	return strings.TrimRight(issuer, "/") + "/.well-known/oauth-authorization-server"
}

// DiscoverResource fetches the protected-resource document for /platform on
// the given MCP host. A 404 is ErrToolsetNotEnabled, so a caller can tell
// "switched off" from "broken".
func DiscoverResource(mcpBaseURL string) (*ProtectedResource, error) {
	u := ResourceMetadataURL(mcpBaseURL)
	var doc ProtectedResource
	if err := getJSON(u, &doc); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.status == http.StatusNotFound {
			return nil, fmt.Errorf("%w (%s answered 404)", ErrToolsetNotEnabled, u)
		}
		return nil, err
	}
	if doc.Resource == "" || len(doc.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("%s is missing resource or authorization_servers", u)
	}
	return &doc, nil
}

// DiscoverAuthServer fetches and sanity-checks the issuer's metadata. The
// issuer member must equal the URL the document was fetched from (RFC 8414
// §3.3): a document that names a different issuer is a document for a
// different server.
func DiscoverAuthServer(issuer string) (*AuthServer, error) {
	issuer = strings.TrimRight(issuer, "/")
	u := AuthServerMetadataURL(issuer)
	var doc AuthServer
	if err := getJSON(u, &doc); err != nil {
		return nil, err
	}
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return nil, fmt.Errorf("%s names issuer %q, expected %q", u, doc.Issuer, issuer)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("%s advertises no authorization or token endpoint — the code flow is not mounted on this issuer", u)
	}
	if !doc.SupportsS256() {
		return nil, fmt.Errorf("%s does not advertise PKCE S256, which is the only method this client uses", u)
	}
	return &doc, nil
}

// httpError is a non-2xx answer to a discovery GET.
type httpError struct {
	url    string
	status int
	body   string
}

func (e *httpError) Error() string {
	if e.body != "" {
		return fmt.Sprintf("HTTP %d from %s: %s", e.status, e.url, e.body)
	}
	return fmt.Sprintf("HTTP %d from %s", e.status, e.url)
}

func getJSON(u string, out any) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", u, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &httpError{url: u, status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", u, err)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
