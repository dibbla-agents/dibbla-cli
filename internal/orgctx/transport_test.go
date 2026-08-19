package orgctx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// recorder is a RoundTripper that captures the request it was handed and
// answers 200, standing in for the network.
type recorder struct {
	got *http.Request
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.got = req
	return &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// newTransport builds a transport with resolution stubbed, so tests never
// touch the OS keyring.
func newTransport(t *testing.T, hosts []string, orgID string) (*transport, *recorder) {
	t.Helper()
	rec := &recorder{}
	set := map[string]bool{}
	for _, h := range hosts {
		set[h] = true
	}
	return &transport{
		base:  rec,
		hosts: set,
		orgID: orgID,
	}, rec
}

// pin marks the lazy resolve as already done, so the stub values above are
// used verbatim.
func pin(tr *transport) *transport {
	tr.once.Do(func() {})
	return tr
}

func request(t *testing.T, url string, headers map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestRoundTrip_AddsOrgHeaderForAuthenticatedAPIRequest(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.dibbla.com/api/deploy/deployments",
		map[string]string{"Authorization": "Bearer tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "org-123" {
		t.Errorf("%s = %q, want org-123", Header, got)
	}
}

// The RoundTripper contract forbids mutating the request it is given; a
// caller that retries would otherwise send a header it never set.
func TestRoundTrip_DoesNotMutateCallerRequest(t *testing.T) {
	tr, _ := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.dibbla.com/api/deploy/deployments",
		map[string]string{"Authorization": "Bearer tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := req.Header.Get(Header); got != "" {
		t.Errorf("caller's request was mutated: %s = %q", Header, got)
	}
}

func TestRoundTrip_SkipsWhenNoOrgSelected(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "")
	req := request(t, "https://api.dibbla.com/api/deploy/deployments",
		map[string]string{"Authorization": "Bearer tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "" {
		t.Errorf("%s = %q, want no header when no org is selected", Header, got)
	}
}

// A third-party host must never see the org id, even on the odd chance the
// request carries a bearer token of its own.
func TestRoundTrip_SkipsForeignHost(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.github.com/repos/dibbla-agents/dibbla-cli/releases/latest",
		map[string]string{"Authorization": "Bearer gh_tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "" {
		t.Errorf("%s leaked to github: %q", Header, got)
	}
}

func TestRoundTrip_SkipsUnauthenticatedRequest(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.dibbla.com/health", nil)

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "" {
		t.Errorf("%s = %q, want no header without a bearer token", Header, got)
	}
}

// An unauthenticated request must not trigger resolution at all — that is
// what keeps `dibbla update` from reading the keyring to download a release.
func TestRoundTrip_UnauthenticatedRequestDoesNotResolve(t *testing.T) {
	origResolve := resolve
	resolved := false
	resolve = func() (map[string]bool, string) {
		resolved = true
		return map[string]bool{"api.dibbla.com": true}, "org-123"
	}
	t.Cleanup(func() { resolve = origResolve })

	tr := &transport{base: &recorder{}}
	req := request(t, "https://github.com/dibbla-agents/dibbla-cli/releases/download/v1/x", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resolved {
		t.Error("resolve was called for an unauthenticated request")
	}
}

func TestRoundTrip_KeepsCallerSuppliedOrgHeader(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.dibbla.com/api/deploy/deployments", map[string]string{
		"Authorization": "Bearer tok",
		Header:          "explicit-org",
	})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "explicit-org" {
		t.Errorf("%s = %q, want the caller's explicit-org", Header, got)
	}
}

// The opt-out must both suppress the org header and strip itself, so the
// marker never reaches the server.
func TestRoundTrip_SkipHeaderSuppressesAndIsStripped(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://api.dibbla.com/api/auth/v1/tokens", map[string]string{
		"Authorization": "Bearer jwt",
		SkipHeader:      "1",
	})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "" {
		t.Errorf("%s = %q, want none when opted out", Header, got)
	}
	if got := rec.got.Header.Get(SkipHeader); got != "" {
		t.Errorf("%s reached the server: %q", SkipHeader, got)
	}
}

func TestRoundTrip_HostMatchIsCaseInsensitive(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com"}, "org-123")
	req := request(t, "https://API.Dibbla.COM/api/deploy/deployments",
		map[string]string{"Authorization": "Bearer tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "org-123" {
		t.Errorf("%s = %q, want org-123", Header, got)
	}
}

// Install must leave existing traffic working, not just add a header.
func TestInstall_WrapsDefaultTransportAndStillServes(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	origResolve := resolve
	resolve = func() (map[string]bool, string) { return nil, "" }
	t.Cleanup(func() { resolve = origResolve })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)

	Install()
	if _, ok := http.DefaultTransport.(*transport); !ok {
		t.Fatalf("DefaultTransport = %T, want *transport", http.DefaultTransport)
	}

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

// The AI gateway is org-aware in its own right, so a selected org must reach
// it — otherwise model traffic is billed to the default org.
func TestRoundTrip_AddsOrgHeaderForGatewayHost(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com", "ai.dibbla.com"}, "org-123")
	req := request(t, "https://ai.dibbla.com/anthropic/v1/messages",
		map[string]string{"Authorization": "Bearer tok"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "org-123" {
		t.Errorf("%s = %q, want org-123 on the gateway host", Header, got)
	}
}

// Widening the set must not turn it into "any host".
func TestRoundTrip_StillSkipsHostsOutsideTheSet(t *testing.T) {
	tr, rec := newTransport(t, []string{"api.dibbla.com", "ai.dibbla.com"}, "org-123")
	req := request(t, "https://api.github.com/repos/x/releases",
		map[string]string{"Authorization": "Bearer gh"})

	if _, err := pin(tr).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.got.Header.Get(Header); got != "" {
		t.Errorf("%s leaked outside the allowlist: %q", Header, got)
	}
}

// The default resolver must produce both hosts from one API URL, since that
// derivation is what the widening relies on.
func TestResolve_IncludesAPIAndGatewayHosts(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")
	t.Setenv("DIBBLA_ORG_ID", "org-abc")
	t.Setenv("DIBBLA_AI_GATEWAY_URL", "")

	hosts, orgID := resolve()
	if orgID != "org-abc" {
		t.Fatalf("orgID = %q, want org-abc", orgID)
	}
	for _, want := range []string{"api.dibbla.com", "ai.dibbla.com"} {
		if !hosts[want] {
			t.Errorf("host %q missing from %v", want, hosts)
		}
	}
}

// An explicit gateway URL must be honoured too, or a self-hosted or dev
// gateway silently loses the header.
func TestResolve_HonoursExplicitGatewayURL(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")
	t.Setenv("DIBBLA_ORG_ID", "org-abc")
	t.Setenv("DIBBLA_AI_GATEWAY_URL", "https://gw.example.test:8443")

	hosts, _ := resolve()
	if !hosts["gw.example.test:8443"] {
		t.Errorf("explicit gateway host missing from %v", hosts)
	}
}

// No org selected means no header anywhere, and no host set to match against.
func TestResolve_NoOrgYieldsNothing(t *testing.T) {
	t.Setenv("DIBBLA_API_TOKEN", "tok")
	t.Setenv("DIBBLA_API_URL", "https://api.dibbla.com")
	t.Setenv("DIBBLA_ORG_ID", "")

	hosts, orgID := resolve()
	if orgID != "" || len(hosts) != 0 {
		t.Errorf("got (%v, %q), want no hosts and no org", hosts, orgID)
	}
}
