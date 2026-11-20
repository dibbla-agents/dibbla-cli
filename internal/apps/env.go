package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// EnvVariable is one variable of an app's resolved environment that is not
// a secret, with its value and the layer it came from: inline (a -e /
// manifest env var) or platform (an injected DIBBLA_* variable).
type EnvVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
	// LooksLikeSecret: the server thinks this inline variable is a secret by
	// its name or value (DIB-1339) — a candidate to move into the secrets
	// store.
	LooksLikeSecret bool `json:"looks_like_secret,omitempty"`
}

// EnvSecret is a secret in the app's environment: its name and the scope it
// resolves from (global | deployment | service). A secret is write-only
// (DIB-1337): the server never sends its value, and this type has no field
// for one.
type EnvSecret struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// EnvEntry is one place a name of the app's environment is set (DIB-1837):
// the read view apps get, env pull, secrets list -d and MCP all show.
type EnvEntry struct {
	Name string `json:"name"`
	// Kind is variable or secret. A variable carries its value; a secret
	// never does, and neither does a variable that shares a secret's name
	// (ValueHidden).
	Kind        string  `json:"kind"`
	Value       *string `json:"value,omitempty"`
	ValueHidden bool    `json:"value_hidden,omitempty"`
	// Scope is global | app | service, or platform for an injected DIBBLA_*.
	Scope string `json:"scope"`
	// Origin is what set it, where known: deploy, console, platform.
	Origin string `json:"origin,omitempty"`
	// Effective is the one the running app gets for Name.
	Effective bool `json:"effective"`
	// Live is false when the value was saved after the app started: a
	// restart applies it.
	Live            bool `json:"live"`
	LooksLikeSecret bool `json:"looks_like_secret,omitempty"`
}

// EnvWarning is a name set in more than one place, with which one the app
// gets. Never a value.
type EnvWarning struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// EnvResponse is GET /deployments/{alias}/env (DIB-919, DIB-1337, DIB-1837).
type EnvResponse struct {
	DeploymentAlias string `json:"deployment_alias"`
	Service         string `json:"service,omitempty"`
	// Entries, Live and Warnings are the read view (DIB-1837); a server from
	// before it sends only Variables and Secrets — see View.
	Entries   []EnvEntry    `json:"entries"`
	Live      *bool         `json:"live,omitempty"`
	Warnings  []EnvWarning  `json:"warnings"`
	Variables []EnvVariable `json:"variables"`
	Secrets   []EnvSecret   `json:"secrets"`
}

// View is the read view's entries; for a server from before DIB-1837 they
// are made from Variables and Secrets (each effective and live, as that
// server could not tell otherwise).
func (r *EnvResponse) View() []EnvEntry {
	if r.Entries != nil {
		return r.Entries
	}
	scope := map[string]string{"deployment": "app", "inline": "app"}
	var out []EnvEntry
	for _, v := range r.Variables {
		value := v.Value
		e := EnvEntry{Name: v.Name, Kind: "variable", Value: &value, Scope: v.Source, Origin: "deploy", Effective: true, Live: true, LooksLikeSecret: v.LooksLikeSecret}
		if s, ok := scope[v.Source]; ok {
			e.Scope = s
		}
		if v.Source == "platform" {
			e.Origin = "platform"
		}
		out = append(out, e)
	}
	for _, s := range r.Secrets {
		e := EnvEntry{Name: s.Name, Kind: "secret", Scope: s.Source, Effective: true, Live: true}
		if sc, ok := scope[s.Source]; ok {
			e.Scope = sc
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetEnv fetches the environment the app's container sees: the variables
// with their values, and the secrets by name only, resolved global <
// deployment < service. A viewer is refused with 403 ROLE_FORBIDDEN.
func GetEnv(apiURL, apiToken, alias, service string) (*EnvResponse, []byte, error) {
	if !AliasRe.MatchString(alias) {
		return nil, nil, fmt.Errorf("invalid alias %q", alias)
	}
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/env", strings.TrimSuffix(apiURL, "/"), alias)
	if service != "" {
		endpoint += "?service=" + url.QueryEscape(service)
	}
	status, raw, err := doChecksRequest(http.MethodGet, endpoint, apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out EnvResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("decode response: %w", err)
	}
	return &out, raw, nil
}
