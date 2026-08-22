package orgs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/orgctx"
)

func sample() []Org {
	return []Org{
		{ID: "id-acme", Name: "Acme", Slug: "acme", Role: "owner"},
		{ID: "id-beta", Name: "Beta Corp", Slug: "beta-corp", Role: "developer"},
	}
}

func TestResolve_ByID(t *testing.T) {
	got, err := Resolve(sample(), "id-beta")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Slug != "beta-corp" {
		t.Errorf("slug = %q, want beta-corp", got.Slug)
	}
}

func TestResolve_BySlug(t *testing.T) {
	got, err := Resolve(sample(), "beta-corp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "id-beta" {
		t.Errorf("id = %q, want id-beta", got.ID)
	}
}

func TestResolve_ByNameIsCaseInsensitive(t *testing.T) {
	got, err := Resolve(sample(), "beta corp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "id-beta" {
		t.Errorf("id = %q, want id-beta", got.ID)
	}
}

func TestResolve_TrimsWhitespace(t *testing.T) {
	if _, err := Resolve(sample(), "  acme  "); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_NoMatchListsWhatIsAvailable(t *testing.T) {
	_, err := Resolve(sample(), "gamma")
	if err == nil {
		t.Fatal("want an error for an unknown organization")
	}
	// The message has to be actionable: a bare "not found" leaves the user
	// guessing at the spelling.
	if !strings.Contains(err.Error(), "Acme") || !strings.Contains(err.Error(), "Beta Corp") {
		t.Errorf("error should list the available orgs, got: %v", err)
	}
}

// Names are not unique. Picking the first match would send work to an org the
// user did not choose, so an ambiguous name must fail.
func TestResolve_AmbiguousNameIsRejected(t *testing.T) {
	list := []Org{
		{ID: "id-1", Name: "Acme", Slug: "acme-one", Role: "owner"},
		{ID: "id-2", Name: "Acme", Slug: "acme-two", Role: "viewer"},
	}
	_, err := Resolve(list, "Acme")
	if err == nil {
		t.Fatal("want an error for an ambiguous name")
	}
	if !strings.Contains(err.Error(), "acme-one") || !strings.Contains(err.Error(), "acme-two") {
		t.Errorf("error should name both candidates, got: %v", err)
	}
}

// A slug or id still resolves when a name collides — that is the escape hatch
// the ambiguity error points at.
func TestResolve_SlugWinsOverAmbiguousName(t *testing.T) {
	list := []Org{
		{ID: "id-1", Name: "Acme", Slug: "acme-one", Role: "owner"},
		{ID: "id-2", Name: "Acme", Slug: "acme-two", Role: "viewer"},
	}
	got, err := Resolve(list, "acme-two")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "id-2" {
		t.Errorf("id = %q, want id-2", got.ID)
	}
}

func TestResolve_EmptyQuery(t *testing.T) {
	if _, err := Resolve(sample(), "   "); err == nil {
		t.Fatal("want an error for an empty query")
	}
}

const listBody = `{
  "organizations": [
    {"organization": {"id": "id-zeta", "name": "Zeta", "slug": "zeta", "org_type": "business"}, "role": "viewer"},
    {"organization": {"id": "id-acme", "name": "Acme", "slug": "acme", "org_type": "personal", "plan": "standard"}, "role": "owner"}
  ],
  "count": 2
}`

func TestList_ParsesNestedShapeAndSortsByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ListPath {
			t.Errorf("path = %q, want %q", r.URL.Path, ListPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listBody))
	}))
	defer srv.Close()

	got, err := List(srv.URL, "tok", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Sorted by name, so Acme precedes Zeta despite the response order.
	if got[0].Name != "Acme" || got[1].Name != "Zeta" {
		t.Errorf("order = %q, %q; want Acme, Zeta", got[0].Name, got[1].Name)
	}
	if got[0].Role != "owner" {
		t.Errorf("role = %q, want owner — the role lives on the membership, not the org", got[0].Role)
	}
	if got[0].OrgType != "personal" {
		t.Errorf("org_type = %q, want personal", got[0].OrgType)
	}
	// Plan (P-0027) decodes when present and stays empty when the server
	// omits it (pre-plan installs).
	if got[0].Plan != "standard" {
		t.Errorf("plan = %q, want standard", got[0].Plan)
	}
	if got[1].Plan != "" {
		t.Errorf("plan = %q, want empty for an org without one", got[1].Plan)
	}
}

// Listing must work even when the pinned org has gone bad, since it is the
// only way to find out what to switch to.
func TestList_OptsOutOfTheOrgHeader(t *testing.T) {
	var sawSkip string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSkip = r.Header.Get(orgctx.SkipHeader)
		_, _ = w.Write([]byte(listBody))
	}))
	defer srv.Close()

	if _, err := List(srv.URL, "tok", false); err != nil {
		t.Fatalf("List: %v", err)
	}
	if sawSkip == "" {
		t.Errorf("request did not carry %s", orgctx.SkipHeader)
	}
}

func TestList_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organizations": [], "count": 0}`))
	}))
	defer srv.Close()

	got, err := List(srv.URL, "tok", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestList_SurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"INVALID_TOKEN"}`))
	}))
	defer srv.Close()

	if _, err := List(srv.URL, "tok", false); err == nil {
		t.Fatal("want an error for a 401 response")
	}
}
