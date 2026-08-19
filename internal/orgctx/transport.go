// Package orgctx attaches the caller's organization to outbound API requests.
//
// The CLI builds requests in roughly fifteen places (internal/apps,
// internal/db, internal/secrets, internal/deploy, internal/applogs,
// internal/vcs, ...), each constructing an *http.Request and setting the
// Authorization header by hand. Threading an org argument through all of them
// would touch every signature on the way, and a call site missed in that sweep
// would keep talking to the account's default org while the user believes they
// switched — silently reading or, worse, writing in the wrong organization.
//
// So the header is attached in exactly one place instead. Every one of those
// call sites uses http.DefaultClient or a zero-valued http.Client, both of
// which round-trip through http.DefaultTransport, so wrapping that transport
// covers all of them at once and covers any added later for free.
package orgctx

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
)

// Header carries the organization id the request should act as. auth-service
// validates membership before honoring it and answers 403 when the caller is
// not a member, so a stale or wrong value fails loudly rather than silently
// falling back to the default org.
const Header = "X-Org-ID"

// SkipHeader marks a request that must go out with no org attached. It is
// stripped before the request leaves, so it never reaches the server.
//
// The login token exchange sets it: at that moment any pinned org still
// belongs to the previous session, and sending it would make logging in to a
// second account fail with "Not a member of this organization" — an error
// about the org, raised while the user is trying to fix their credentials.
const SkipHeader = "X-Dibbla-No-Org"

// resolve reports the API host to scope the header to and the org to send.
// Indirected for tests, which have no keyring to read.
var resolve = func() (apiHost, orgID string) {
	cfg := config.Load()
	if cfg.OrgID == "" {
		return "", ""
	}
	u, err := url.Parse(cfg.APIURL)
	if err != nil {
		return "", ""
	}
	return strings.ToLower(u.Host), cfg.OrgID
}

type transport struct {
	base http.RoundTripper

	once    sync.Once
	apiHost string
	orgID   string
}

// Install wraps http.DefaultTransport so requests to the configured API host
// carry the active organization. Safe to call once at startup; commands that
// never reach the API are unaffected.
func Install() {
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	http.DefaultTransport = &transport{base: base}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(SkipHeader) != "" {
		out := req.Clone(req.Context())
		out.Header.Del(SkipHeader)
		out.Header.Del(Header)
		return t.base.RoundTrip(out)
	}

	// Only authenticated Dibbla calls are candidates. Checking this before
	// resolving matters: `dibbla update` downloads release assets from GitHub
	// without an Authorization header, and this check keeps that path from
	// touching the OS keyring (and prompting for it) to answer a question
	// about a host we were never going to add the header to.
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		return t.base.RoundTrip(req)
	}

	// A caller that set the header itself has said what it wants.
	if req.Header.Get(Header) != "" {
		return t.base.RoundTrip(req)
	}

	t.once.Do(func() { t.apiHost, t.orgID = resolve() })
	if t.orgID == "" || t.apiHost == "" {
		return t.base.RoundTrip(req)
	}
	if req.URL == nil || !strings.EqualFold(req.URL.Host, t.apiHost) {
		return t.base.RoundTrip(req)
	}

	// RoundTrippers must not modify the request they are given.
	out := req.Clone(req.Context())
	out.Header.Set(Header, t.orgID)
	return t.base.RoundTrip(out)
}
