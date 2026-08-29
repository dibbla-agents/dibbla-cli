package platformoauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Grant is what a successful authorization leaves behind, and what every later
// call — refresh, revoke, introspect, the MCP probe — is made with. It is
// stored as one JSON blob per context (internal/credential), never split
// across files: the access token without its refresh token is a credential
// that dies in an hour, and the refresh token without the issuer it belongs to
// is a credential nobody can use.
type Grant struct {
	// Issuer and Resource are what the grant was minted for. They are checked
	// against discovery on every use, so a grant obtained against one install
	// is never presented to another.
	Issuer   string `json:"issuer"`
	Resource string `json:"resource"`
	ClientID string `json:"client_id"`

	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
	ObtainedAt   time.Time `json:"obtained_at"`

	// The endpoints as discovered at login, so refresh and revoke need no
	// discovery round-trip. Re-discovery replaces them.
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint,omitempty"`
	IntrospectionEndpoint string `json:"introspection_endpoint,omitempty"`
}

// expirySkew is how early a token is treated as expired, so a check that says
// "valid" is not contradicted by a call made a few seconds later.
const expirySkew = 30 * time.Second

// Expired reports whether the access token is (about to be) past its expiry.
func (g *Grant) Expired(now time.Time) bool {
	return !now.Add(expirySkew).Before(g.ExpiresAt)
}

// Scopes returns the granted scopes as a list.
func (g *Grant) Scopes() []string {
	return strings.Fields(g.Scope)
}

// Encode serialises the grant for storage.
func (g *Grant) Encode() ([]byte, error) {
	return json.Marshal(g)
}

// Decode reads a stored grant. A payload that does not decode is reported
// rather than treated as "no grant": the user did log in, and silently asking
// them to do so again hides a bug.
func Decode(raw []byte) (*Grant, error) {
	var g Grant
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("stored platform grant is not readable: %w", err)
	}
	if g.AccessToken == "" || g.Issuer == "" || g.Resource == "" {
		return nil, errors.New("stored platform grant is incomplete")
	}
	return &g, nil
}

// tokenResponse is the token endpoint's success body (RFC 6749 §5.1).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

// applyTokenResponse folds a token response into the grant. A rotation that
// returns no refresh token keeps the old one only when the server did not
// advertise rotation; Dibbla always rotates, so an absent refresh token after
// a refresh means "this was the last one".
func (g *Grant) applyTokenResponse(tr tokenResponse, now time.Time) {
	g.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		g.RefreshToken = tr.RefreshToken
	}
	if tr.Scope != "" {
		g.Scope = tr.Scope
	}
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	g.ExpiresAt = now.Add(ttl)
	g.ObtainedAt = now
}
