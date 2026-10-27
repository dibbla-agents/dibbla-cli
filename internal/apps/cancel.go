package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CancelResponse is POST /deployments/{alias}/cancel. Cancelled false means
// nothing was deploying. Accepted is the 202 answer: the deploy runs on the
// other deploy-api process of a rollout and stops at its next lock
// heartbeat, within seconds (DIB-1164).
type CancelResponse struct {
	Alias        string `json:"alias"`
	Cancelled    bool   `json:"cancelled"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Message      string `json:"message"`
	Accepted     bool   `json:"-"`
}

// CancelDeploy stops the deploy in progress for alias, if any, and frees the
// app's deploy lock (DIB-1616) — until then nothing in the CLI called the
// endpoint, and a hung deploy held the app for its whole rollout wait.
func CancelDeploy(apiURL, apiToken, alias string) (*CancelResponse, error) {
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/cancel",
		strings.TrimSuffix(apiURL, "/"), url.PathEscape(alias))
	status, body, err := doChecksRequest(http.MethodPost, endpoint, apiToken, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return nil, statusError(status, body)
	}
	var out CancelResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	out.Accepted = status == http.StatusAccepted
	return &out, nil
}
