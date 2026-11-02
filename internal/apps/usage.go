package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// UsageReading is one resource of a pod in GET /deployments/{alias}/usage:
// CPU in millicores or memory in bytes. Usage is nil when the cluster had no
// sample; Limit 0 means no limit.
type UsageReading struct {
	Usage            *int64   `json:"usage"`
	Request          int64    `json:"request"`
	Limit            int64    `json:"limit"`
	PercentOfRequest *float64 `json:"percent_of_request"`
	PercentOfLimit   *float64 `json:"percent_of_limit"`
}

// PodUsage is one pod of the app.
type PodUsage struct {
	Service       string       `json:"service"`
	Pod           string       `json:"pod"`
	State         string       `json:"state"`
	Restarts      int          `json:"restarts"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	AgeSeconds    int64        `json:"age_seconds"`
	WarmingUp     bool         `json:"warming_up"`
	CPU           UsageReading `json:"cpu"`
	Memory        UsageReading `json:"memory"`
	WindowSeconds float64      `json:"window_seconds,omitempty"`
}

// UsageResponse is the usage document (DIB-1645).
type UsageResponse struct {
	Alias            string     `json:"deployment_alias"`
	MetricsAvailable bool       `json:"metrics_available"`
	MetricsError     string     `json:"metrics_error,omitempty"`
	WarmupSeconds    int64      `json:"warmup_seconds"`
	Pods             []PodUsage `json:"pods"`
}

// GetUsage fetches what each pod of the app uses of CPU and memory right
// now, next to its request and limit. The raw body is returned beside the
// decoded document so --json can emit the server's contract verbatim.
func GetUsage(apiURL, apiToken, alias string) (*UsageResponse, []byte, error) {
	if !AliasRe.MatchString(alias) {
		return nil, nil, fmt.Errorf("invalid alias %q", alias)
	}
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/usage", strings.TrimSuffix(apiURL, "/"), alias)
	status, raw, err := doChecksRequest(http.MethodGet, endpoint, apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out UsageResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("decode response: %w", err)
	}
	return &out, raw, nil
}
