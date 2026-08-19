// Package orgs reads the organizations the logged-in user belongs to and
// resolves the name a user types to the id the API expects.
package orgs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/orgctx"
)

// ListPath is the gateway route for auth-service's GET /api/v1/tokens/orgs.
const ListPath = "/api/auth/v1/tokens/orgs"

// Org is one organization the caller belongs to, with the role they hold in it.
type Org struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	OrgType string `json:"org_type"`
	Role    string `json:"role"`
}

// listResponse mirrors auth-service's ListOrgs body: the membership row is the
// outer object and the organization is nested inside it, because the role
// belongs to the membership rather than to the org.
type listResponse struct {
	Organizations []struct {
		Organization struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Slug    string `json:"slug"`
			OrgType string `json:"org_type"`
		} `json:"organization"`
		Role string `json:"role"`
	} `json:"organizations"`
}

// List returns the caller's organizations, sorted by name so repeated runs
// print the same order.
//
// The request opts out of the org header. Listing is the one call that has to
// work when the pinned org has gone bad — a user removed from the org they
// pinned would otherwise get 403 from the membership check and have no way to
// see what they could switch to, i.e. the recovery path would be broken by
// exactly the state it exists to recover from.
func List(apiURL, token string, verbose bool) ([]Org, error) {
	client := apiclient.NewClient(apiURL, token, verbose)
	resp, err := client.GetWithHeaders(ListPath, map[string]string{orgctx.SkipHeader: "1"})
	if err != nil {
		return nil, err
	}

	var body listResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, fmt.Errorf("could not parse organization list: %w", err)
	}

	out := make([]Org, 0, len(body.Organizations))
	for _, entry := range body.Organizations {
		out = append(out, Org{
			ID:      entry.Organization.ID,
			Name:    entry.Organization.Name,
			Slug:    entry.Organization.Slug,
			OrgType: entry.Organization.OrgType,
			Role:    entry.Role,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Name, out[j].Name) {
			return out[i].ID < out[j].ID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Resolve maps what the user typed to exactly one organization.
//
// Ids and slugs are unique, so they are tried first and a hit is returned
// immediately. Names are not unique — two orgs may legitimately share one — so
// a name that matches more than once is reported as ambiguous rather than
// resolved to whichever came back first.
func Resolve(list []Org, query string) (Org, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Org{}, fmt.Errorf("no organization given")
	}

	for _, o := range list {
		if strings.EqualFold(o.ID, q) || strings.EqualFold(o.Slug, q) {
			return o, nil
		}
	}

	var byName []Org
	for _, o := range list {
		if strings.EqualFold(o.Name, q) {
			byName = append(byName, o)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Org{}, fmt.Errorf("no organization matches %q\n\nYou belong to:\n%s", q, Format(list))
	default:
		return Org{}, fmt.Errorf("%q matches %d organizations; use the slug or id instead:\n%s",
			q, len(byName), Format(byName))
	}
}

// Format renders orgs as indented "name (slug) — role  id" lines for use
// inside error messages.
func Format(list []Org) string {
	var b strings.Builder
	for _, o := range list {
		fmt.Fprintf(&b, "  %s (%s) — %s  %s\n", o.Name, o.Slug, o.Role, o.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}
