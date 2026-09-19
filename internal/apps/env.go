package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// EnvVariable is one variable of an app's resolved environment as the
// container sees it, with the layer it came from: global | deployment |
// service (the secret scopes), inline (a -e / manifest env var) or platform
// (an injected DIBBLA_* variable).
type EnvVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// EnvResponse is GET /deployments/{alias}/env (DIB-919).
type EnvResponse struct {
	DeploymentAlias string        `json:"deployment_alias"`
	Service         string        `json:"service,omitempty"`
	Variables       []EnvVariable `json:"variables"`
}

// GetEnv fetches the environment the app's container sees — secrets resolved
// global < deployment < service, plus inline env and the injected DIBBLA_*
// variables — values included. A viewer is refused with 403 ROLE_FORBIDDEN,
// the same rule as reading a single secret.
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
