package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// EnvVariable is one variable of an app's resolved environment that is not
// a secret, with its value and the layer it came from: inline (a -e /
// manifest env var) or platform (an injected DIBBLA_* variable).
type EnvVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// EnvSecret is a secret in the app's environment: its name and the scope it
// resolves from (global | deployment | service). A secret is write-only
// (DIB-1337): the server never sends its value, and this type has no field
// for one.
type EnvSecret struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// EnvResponse is GET /deployments/{alias}/env (DIB-919, DIB-1337).
type EnvResponse struct {
	DeploymentAlias string        `json:"deployment_alias"`
	Service         string        `json:"service,omitempty"`
	Variables       []EnvVariable `json:"variables"`
	Secrets         []EnvSecret   `json:"secrets"`
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
