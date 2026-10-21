package apps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PlanStatus is the part of deploy-api's GET /plan that `dibbla status`
// reports for a Hobby org (DIB-1048, DIB-1519): the page that upgrades it to
// Business. The CLI has no console URL of its own, so the link can only come
// from the server. The trial fields an older server still sends are ignored.
type PlanStatus struct {
	Plan       string `json:"plan"`
	UpgradeURL string `json:"upgrade_url"`
}

// planStatusTimeout keeps `dibbla status` quick when deploy-api is slow: the
// plan line is a nicety next to the login check.
const planStatusTimeout = 5 * time.Second

// GetPlanStatus reads GET /api/deploy/plan for the pinned org.
func GetPlanStatus(apiURL, apiToken string) (*PlanStatus, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(apiURL, "/")+"/api/deploy/plan", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")
	res, err := (&http.Client{Timeout: planStatusTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, statusError(res.StatusCode, body)
	}
	var out PlanStatus
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode plan status: %w", err)
	}
	return &out, nil
}
