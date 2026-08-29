package platformoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubIssuer is a minimal authorization server: RFC 8414 metadata, an
// authorize endpoint that redirects straight back with a code (no login, no
// consent page — those are the server's tests), a token endpoint that checks
// PKCE and the resource, and introspection/revocation. It records what it saw.
type stubIssuer struct {
	srv       *httptest.Server
	mu        sync.Mutex
	challenge string
	code      string
	redirect  string
	resource  string
	refresh   string
	revoked   bool
	active    bool
	tokenForm url.Values
}

func newStubIssuer(t *testing.T) *stubIssuer {
	t.Helper()
	s := &stubIssuer{code: "code-1", refresh: "rt-1", active: true}
	mux := http.NewServeMux()
	// The same server plays the resource server too, so the resource it
	// names is the one the issuer binds tokens to.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/platform", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": s.resource, "authorization_servers": []string{s.srv.URL}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                s.srv.URL,
			"authorization_endpoint":                s.srv.URL + "/oauth/authorize",
			"token_endpoint":                        s.srv.URL + "/oauth/token",
			"revocation_endpoint":                   s.srv.URL + "/oauth/revoke",
			"introspection_endpoint":                s.srv.URL + "/oauth/introspect",
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s.mu.Lock()
		s.challenge = q.Get("code_challenge")
		s.redirect = q.Get("redirect_uri")
		s.mu.Unlock()
		if q.Get("code_challenge_method") != "S256" || q.Get("resource") != s.resource || q.Get("client_id") == "" {
			http.Error(w, "bad authorize request: "+r.URL.RawQuery, 400)
			return
		}
		u, _ := url.Parse(q.Get("redirect_uri"))
		rq := url.Values{}
		rq.Set("code", s.code)
		rq.Set("state", q.Get("state"))
		rq.Set("iss", s.srv.URL)
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.tokenForm = r.PostForm
		s.mu.Unlock()
		fail := func(code, desc string) {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != s.challenge {
				fail("invalid_grant", "code_verifier does not match")
				return
			}
			if r.PostForm.Get("resource") != s.resource || r.PostForm.Get("redirect_uri") != s.redirect {
				fail("invalid_grant", "binding mismatch")
				return
			}
		case "refresh_token":
			if s.revoked || r.PostForm.Get("refresh_token") != s.refresh {
				fail("invalid_grant", "this grant has been revoked")
				return
			}
			s.refresh = "rt-rotated"
		default:
			fail("unsupported_grant_type", "")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-" + r.PostForm.Get("grant_type"), "token_type": "Bearer",
			"expires_in": 3600, "scope": "platform:identity:read", //contract-pinned: stub server output
			"refresh_token": s.refresh,
		})
	})
	mux.HandleFunc("POST /oauth/introspect", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": s.active && !s.revoked, "exp": time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("POST /oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		s.revoked = true
		w.WriteHeader(200)
	})
	s.srv = httptest.NewServer(mux)
	s.resource = s.srv.URL + "/platform"
	t.Cleanup(s.srv.Close)
	return s
}

// stubResource serves the RFC 9728 document that points at the issuer.
func stubResource(t *testing.T, issuer string, enabled bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled || r.URL.Path != "/.well-known/oauth-protected-resource/platform" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "http://" + r.Host + "/platform",
			"authorization_servers": []string{issuer},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// browse plays the browser: follows the authorize redirect to the loopback
// callback, like a real user agent would.
func browse(t *testing.T, u string) {
	t.Helper()
	client := &http.Client{}
	resp, err := client.Get(u)
	if err != nil {
		t.Errorf("browse %s: %v", u, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("browser ended on HTTP %d", resp.StatusCode)
	}
}

func TestLoginRoundTrip(t *testing.T) {
	issuer2 := newStubIssuer(t)
	resource := issuer2.resource
	g, err := Login(context.Background(), issuer2.srv.URL, AuthorizeOptions{
		ClientID: "dibbla-cli",
		OpenURL:  func(u string) { go browse(t, u) },
		Ports:    []int{0},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if g.AccessToken != "at-authorization_code" || g.RefreshToken != "rt-1" {
		t.Errorf("unexpected grant %+v", g)
	}
	if g.Resource != resource || g.Issuer != issuer2.srv.URL || g.ClientID != "dibbla-cli" {
		t.Errorf("grant bindings wrong: %+v", g)
	}
	if g.Expired(time.Now()) {
		t.Error("a fresh grant must not read as expired")
	}
	form := issuer2.tokenForm
	if form.Get("client_secret") != "" {
		t.Error("a public client must never send a client_secret")
	}
	if form.Get("resource") != resource || form.Get("code_verifier") == "" {
		t.Errorf("token request missing resource or verifier: %v", form)
	}
	if !strings.HasPrefix(form.Get("redirect_uri"), "http://127.0.0.1:") || !strings.HasSuffix(form.Get("redirect_uri"), CallbackPath) {
		t.Errorf("redirect_uri must be a literal loopback callback: %s", form.Get("redirect_uri"))
	}

	// Refresh rotates: new access token, new refresh token.
	if err := Refresh(g); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if g.AccessToken != "at-refresh_token" || g.RefreshToken != "rt-rotated" {
		t.Errorf("rotation not applied: %+v", g)
	}

	in, err := Introspect(g)
	if err != nil || !in.Active {
		t.Fatalf("Introspect: active=%v err=%v", in != nil && in.Active, err)
	}

	// Revoke, then the old refresh token is refused as invalid_grant and
	// introspection says inactive — the two signals the check maps to "run
	// --login".
	if err := Revoke(g); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	err = Refresh(g)
	if !IsInvalidGrant(err) {
		t.Errorf("refresh after revoke must be invalid_grant, got %v", err)
	}
	in, err = Introspect(g)
	if err != nil || in.Active {
		t.Errorf("introspection after revoke must be inactive, got active=%v err=%v", in != nil && in.Active, err)
	}
}

func TestDiscoverResourceNotEnabled(t *testing.T) {
	res := stubResource(t, "http://issuer.invalid", false)
	_, err := DiscoverResource(res.URL)
	if err == nil || !strings.Contains(err.Error(), ErrToolsetNotEnabled.Error()) {
		t.Fatalf("want ErrToolsetNotEnabled, got %v", err)
	}
}

func TestDiscoverAuthServerRejectsForeignIssuer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": "https://somebody-else.example", "authorization_endpoint": "x", "token_endpoint": "y",
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer srv.Close()
	if _, err := DiscoverAuthServer(srv.URL); err == nil || !strings.Contains(err.Error(), "names issuer") {
		t.Fatalf("want issuer mismatch, got %v", err)
	}
}

func TestDiscoverAuthServerRequiresS256(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": "x", "token_endpoint": "y",
			"code_challenge_methods_supported": []string{"plain"},
		})
	}))
	defer srv.Close()
	if _, err := DiscoverAuthServer(srv.URL); err == nil || !strings.Contains(err.Error(), "S256") {
		t.Fatalf("want S256 refusal, got %v", err)
	}
}

func TestCallbackRefusesWrongStateAndForeignIssuer(t *testing.T) {
	l, port, err := listenLoopback([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan callbackResult, 1)
	srv := serveCallback(l, "good-state", "https://issuer.example", out)
	defer srv.Shutdown(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d%s", port, CallbackPath)

	resp, err := http.Get(base + "?state=bad&code=c")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong state must be 403, got %d", resp.StatusCode)
	}
	select {
	case r := <-out:
		t.Errorf("wrong state must not deliver a result, got %+v", r)
	default:
	}

	resp, err = http.Get(base + "?state=good-state&code=c&iss=https://evil.example")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	r := <-out
	if r.err == nil || !strings.Contains(r.err.Error(), "issuer") {
		t.Errorf("foreign iss must fail the flow, got %+v", r)
	}
}

func TestCallbackReportsServerRefusal(t *testing.T) {
	l, port, err := listenLoopback([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan callbackResult, 1)
	srv := serveCallback(l, "s", "https://issuer.example", out)
	defer srv.Shutdown(context.Background())
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s?state=s&error=access_denied&error_description=the+user+declined", port, CallbackPath))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	r := <-out
	if r.err == nil || !strings.Contains(r.err.Error(), "access_denied") || !strings.Contains(r.err.Error(), "declined") {
		t.Errorf("refusal must carry code and description, got %+v", r)
	}
}

func TestGrantEncodeDecode(t *testing.T) {
	g := &Grant{Issuer: "i", Resource: "r", ClientID: "c", AccessToken: "a", RefreshToken: "b", ExpiresAt: time.Now().Add(time.Hour), Scope: "x y"}
	raw, err := g.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.AccessToken != "a" || len(back.Scopes()) != 2 {
		t.Errorf("round trip lost data: %+v", back)
	}
	if _, err := Decode([]byte(`{"issuer":"i"}`)); err == nil {
		t.Error("an incomplete grant must not decode")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Error("garbage must not decode")
	}
}

func TestPKCEIsS256(t *testing.T) {
	p, err := newPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(p.verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge {
		t.Error("challenge is not S256 of the verifier")
	}
	if len(p.verifier) < 43 {
		t.Errorf("verifier too short for RFC 7636: %d", len(p.verifier))
	}
}
