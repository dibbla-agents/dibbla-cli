package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Release is one entry of GET /deployments/{alias}/releases: an image the
// registry still holds under the app's immutable dep_… tag, plus what
// deploy-api remembers about it.
type Release struct {
	DeploymentID string     `json:"deployment_id"`
	Image        string     `json:"image"`
	Digest       string     `json:"digest,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	AuthorEmail  string     `json:"author_email,omitempty"`
	Running      bool       `json:"running"`
	Available    bool       `json:"available"`
	HasConfig    bool       `json:"has_config"`
}

// ReleasesResponse is the releases document.
type ReleasesResponse struct {
	Alias                string    `json:"alias"`
	DeploymentExists     bool      `json:"deployment_exists"`
	RunningDeploymentID  string    `json:"running_deployment_id,omitempty"`
	PreviousDeploymentID string    `json:"previous_deployment_id,omitempty"`
	RegistryReachable    bool      `json:"registry_reachable"`
	Releases             []Release `json:"releases"`
}

// RollbackResponse is what POST /deployments/{alias}/rollback answers.
type RollbackResponse struct {
	Alias                string `json:"alias"`
	Status               string `json:"status"`
	DeploymentID         string `json:"deployment_id"`
	PreviousDeploymentID string `json:"previous_deployment_id,omitempty"`
	Image                string `json:"image"`
	Recreated            bool   `json:"recreated"`
	Message              string `json:"message"`
}

// ListReleases fetches the app's releases. The raw body is returned beside
// the decoded document so --json can emit the server's contract verbatim.
func ListReleases(apiURL, apiToken, alias string) (*ReleasesResponse, []byte, error) {
	if !AliasRe.MatchString(alias) {
		return nil, nil, fmt.Errorf("invalid alias %q", alias)
	}
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/releases", strings.TrimSuffix(apiURL, "/"), alias)
	status, raw, err := doChecksRequest(http.MethodGet, endpoint, apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out ReleasesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("decode response: %w", err)
	}
	return &out, raw, nil
}

// Rollback switches the app to the named release, or to the previous one
// when to is empty. No build happens: the image must already be in the
// registry, and a swept one answers 410 RELEASE_GONE.
func Rollback(apiURL, apiToken, alias, to string) (*RollbackResponse, []byte, error) {
	if !AliasRe.MatchString(alias) {
		return nil, nil, fmt.Errorf("invalid alias %q", alias)
	}
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/rollback", strings.TrimSuffix(apiURL, "/"), alias)
	var body any
	if to != "" {
		body = map[string]string{"to": to}
	}
	status, raw, err := doChecksRequest(http.MethodPost, endpoint, apiToken, body)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out RollbackResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("decode response: %w", err)
	}
	return &out, raw, nil
}
