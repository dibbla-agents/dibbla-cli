package platformoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- PKCE --------------------------------------------------------------------

// pkce is one verifier/challenge pair. RFC 7636: 32 random bytes, base64url
// without padding, S256 challenge.
type pkce struct {
	verifier  string
	challenge string
}

func newPKCE() (pkce, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return pkce{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(v))
	return pkce{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- the authorization-code flow ---------------------------------------------

// AuthorizeOptions is everything a login needs that discovery did not supply.
type AuthorizeOptions struct {
	ClientID string
	// Scope is the space-separated scope request. Empty asks the server for
	// its consent defaults (the read scopes), which is what the CLI sends: it
	// does not hold a scope list of its own.
	Scope string
	// OpenURL is called with the authorization URL once the callback listener
	// is up. It is the caller's job to open a browser or print the URL; this
	// package does not know what a terminal is.
	OpenURL func(u string)
	// Timeout bounds the wait for the browser; zero means five minutes.
	Timeout time.Duration
	// Ports overrides CallbackPorts; tests bind port 0 to get any free one.
	Ports []int
}

// Login runs the whole authorization: discovery on both hosts, the loopback
// listener, the browser leg, the code exchange. It returns a grant ready to be
// stored.
func Login(ctx context.Context, mcpBaseURL string, opts AuthorizeOptions) (*Grant, error) {
	pr, err := DiscoverResource(mcpBaseURL)
	if err != nil {
		return nil, err
	}
	as, err := DiscoverAuthServer(pr.AuthorizationServers[0])
	if err != nil {
		return nil, err
	}
	return Authorize(ctx, as, pr.Resource, opts)
}

// Authorize is Login after discovery: exported so a caller that has already
// discovered (the check command) does not fetch the documents twice.
func Authorize(ctx context.Context, as *AuthServer, resource string, opts AuthorizeOptions) (*Grant, error) {
	if opts.ClientID == "" {
		opts.ClientID = DefaultClientID
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	ports := opts.Ports
	if len(ports) == 0 {
		ports = CallbackPorts
	}

	p, err := newPKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	listener, port, err := listenLoopback(ports)
	if err != nil {
		return nil, err
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, CallbackPath)

	resultCh := make(chan callbackResult, 1)
	srv := serveCallback(listener, state, as.Issuer, resultCh)
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", opts.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", p.challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", resource)
	if s := strings.TrimSpace(opts.Scope); s != "" {
		q.Set("scope", s)
	}
	authURL := as.AuthorizationEndpoint + "?" + q.Encode()
	if opts.OpenURL != nil {
		opts.OpenURL(authURL)
	}

	var res callbackResult
	select {
	case res = <-resultCh:
	case <-ctx.Done():
		return nil, fmt.Errorf("timed out after %s waiting for the browser to return to %s", opts.Timeout, redirectURI)
	}
	if res.err != nil {
		return nil, res.err
	}

	g := &Grant{
		Issuer:                as.Issuer,
		Resource:              resource,
		ClientID:              opts.ClientID,
		TokenEndpoint:         as.TokenEndpoint,
		RevocationEndpoint:    as.RevocationEndpoint,
		IntrospectionEndpoint: as.IntrospectionEndpoint,
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", res.code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", opts.ClientID)
	form.Set("code_verifier", p.verifier)
	form.Set("resource", resource)
	tr, err := postToken(as.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("redeeming the authorization code: %w", err)
	}
	g.applyTokenResponse(tr, time.Now())
	if g.AccessToken == "" {
		return nil, errors.New("the token endpoint returned no access token")
	}
	// The listener lingers under the caller's control (defer above); the
	// browser has already been answered by the time we get here.
	return g, nil
}

// listenLoopback binds the first port in the list that is free. Port 0 asks
// the OS for any port, which only a registration with a wildcard could match;
// it exists for tests.
func listenLoopback(ports []int) (net.Listener, int, error) {
	var lastErr error
	for _, port := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			lastErr = err
			continue
		}
		return l, l.Addr().(*net.TCPAddr).Port, nil
	}
	return nil, 0, fmt.Errorf("no loopback callback port is free (tried %v): %w", ports, lastErr)
}

type callbackResult struct {
	code string
	err  error
}

// serveCallback answers exactly one authorization response on the listener.
// The state must match, and — RFC 9207 — the iss parameter, when present,
// must name the issuer the request was sent to, so a response from another
// server cannot be injected into this flow.
func serveCallback(l net.Listener, state, issuer string, out chan<- callbackResult) *http.Server {
	var once sync.Once
	deliver := func(r callbackResult) { once.Do(func() { out <- r }) }

	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusForbidden)
			return
		}
		if iss := q.Get("iss"); iss != "" && strings.TrimRight(iss, "/") != strings.TrimRight(issuer, "/") {
			http.Error(w, "issuer mismatch", http.StatusForbidden)
			deliver(callbackResult{err: fmt.Errorf("the authorization response names issuer %q, expected %q", iss, issuer)})
			return
		}
		if e := q.Get("error"); e != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, pageHTML("Authorization failed", "Dibbla refused the request: "+e+". You can close this tab."))
			desc := q.Get("error_description")
			if desc == "" {
				desc = e
			}
			deliver(callbackResult{err: fmt.Errorf("authorization refused (%s): %s", e, desc)})
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "no code", http.StatusBadRequest)
			deliver(callbackResult{err: errors.New("the authorization response carried no code")})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, pageHTML("Connected", "The Dibbla CLI is now connected to the platform. You can close this tab and return to your terminal."))
		deliver(callbackResult{code: code})
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			deliver(callbackResult{err: fmt.Errorf("callback listener: %w", err)})
		}
	}()
	return srv
}

// pageHTML is the minimal callback page: no external resources, so nothing
// about the callback leaks through a referer.
func pageHTML(title, body string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>` + title + `</title></head>` +
		`<body style="font-family:system-ui,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#111;color:#fff">` +
		`<div style="text-align:center;max-width:32em"><h2>` + title + `</h2><p>` + body + `</p></div></body></html>`
}

// --- token endpoint ----------------------------------------------------------

// OAuthError is the token/revocation/introspection endpoints' error body
// (RFC 6749 §5.2), kept typed so a caller can act on the code: invalid_grant
// on a refresh means the grant is gone and the answer is a new login, not a
// retry.
type OAuthError struct {
	Status      int
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status)
}

// IsInvalidGrant reports whether err is the server saying the grant, code or
// refresh token is no longer usable.
func IsInvalidGrant(err error) bool {
	var oe *OAuthError
	return errors.As(err, &oe) && oe.Code == "invalid_grant"
}

func postForm(endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot reach %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func postToken(endpoint string, form url.Values) (tokenResponse, error) {
	status, raw, err := postForm(endpoint, form)
	if err != nil {
		return tokenResponse{}, err
	}
	if status != http.StatusOK {
		oe := &OAuthError{Status: status}
		if json.Unmarshal(raw, oe) != nil || oe.Code == "" {
			oe.Code = "http_" + fmt.Sprint(status)
			oe.Description = strings.TrimSpace(string(raw))
		}
		return tokenResponse{}, oe
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("token endpoint answered with something that is not JSON: %w", err)
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "Bearer") {
		return tokenResponse{}, fmt.Errorf("token endpoint issued a %q token; only Bearer is usable here", tr.TokenType)
	}
	return tr, nil
}

// Refresh rotates the grant: the refresh token is consumed and both tokens are
// replaced in place. An invalid_grant answer means the grant is gone — revoked,
// expired, or its family killed by a replay — and IsInvalidGrant reports it.
func Refresh(g *Grant) error {
	if g.RefreshToken == "" {
		return &OAuthError{Status: 0, Code: "invalid_grant", Description: "this grant holds no refresh token"}
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", g.RefreshToken)
	form.Set("client_id", g.ClientID)
	tr, err := postToken(g.TokenEndpoint, form)
	if err != nil {
		return err
	}
	g.applyTokenResponse(tr, time.Now())
	return nil
}

// Introspection is the RFC 7662 answer for a token.
type Introspection struct {
	Active   bool   `json:"active"`
	Scope    string `json:"scope"`
	ClientID string `json:"client_id"`
	Exp      int64  `json:"exp"`
}

// ErrNoIntrospection means the server advertised no introspection endpoint;
// the caller falls back to "try it and see".
var ErrNoIntrospection = errors.New("the issuer advertises no introspection endpoint")

// Introspect asks the issuer whether the access token is still honoured. This
// is the only way to tell a revoked grant from a live one before the token's
// own expiry: the JWT verifies either way.
func Introspect(g *Grant) (*Introspection, error) {
	if g.IntrospectionEndpoint == "" {
		return nil, ErrNoIntrospection
	}
	form := url.Values{}
	form.Set("token", g.AccessToken)
	form.Set("token_type_hint", "access_token")
	form.Set("client_id", g.ClientID)
	status, raw, err := postForm(g.IntrospectionEndpoint, form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		oe := &OAuthError{Status: status}
		if json.Unmarshal(raw, oe) != nil || oe.Code == "" {
			oe.Code = "http_" + fmt.Sprint(status)
			oe.Description = strings.TrimSpace(string(raw))
		}
		return nil, oe
	}
	var in Introspection
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("introspection endpoint answered with something that is not JSON: %w", err)
	}
	return &in, nil
}

// Revoke ends the grant server-side (RFC 7009). Posting the refresh token
// revokes the whole grant, access tokens included — Dibbla treats revocation
// as the disconnect. Best-effort by contract: RFC 7009 answers 200 for a
// token that is already gone, so the caller forgets the grant locally either
// way.
func Revoke(g *Grant) error {
	if g.RevocationEndpoint == "" {
		return errors.New("the issuer advertises no revocation endpoint")
	}
	tok, hint := g.RefreshToken, "refresh_token"
	if tok == "" {
		tok, hint = g.AccessToken, "access_token"
	}
	form := url.Values{}
	form.Set("token", tok)
	form.Set("token_type_hint", hint)
	form.Set("client_id", g.ClientID)
	status, raw, err := postForm(g.RevocationEndpoint, form)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("revocation answered HTTP %d: %s", status, strings.TrimSpace(string(raw)))
	}
	return nil
}
