package apps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ServiceNameRe is the canonical service-name pattern enforced both client-
// and server-side. Exposed so callers can validate input before issuing an
// HTTP request.
var ServiceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// RestartServiceResponse is the payload returned by POST
// /deployments/{alias}/services/{service}/restart on success.
type RestartServiceResponse struct {
	Alias   string `json:"alias"`
	Service string `json:"service"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// RestartService triggers a K8s rolling restart of the named service inside
// alias's deployment. Returns the parsed response on 200, or a wrapped error
// (with the server error code preserved in the message) for any other status.
func RestartService(apiURL, apiToken, alias, service string) (*RestartServiceResponse, error) {
	if !ServiceNameRe.MatchString(service) {
		return nil, fmt.Errorf("service name %q does not match %s", service, ServiceNameRe.String())
	}
	url := fmt.Sprintf("%s/api/deploy/deployments/%s/services/%s/restart",
		strings.TrimSuffix(apiURL, "/"), alias, service)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		var out RestartServiceResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
		}
		return &out, nil
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Code != "" {
		return nil, fmt.Errorf("API error (%s): %s", errResp.Error.Code, errResp.Error.Message)
	}
	return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
}
