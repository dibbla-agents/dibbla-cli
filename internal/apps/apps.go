package apps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DeploymentsListResponse represents the API response for listing deployments.
type DeploymentsListResponse struct {
	Deployments []Deployment `json:"deployments"`
	Total       int          `json:"total"`
}

// Deployment represents a single application deployment.
type Deployment struct {
	ID           string           `json:"id"`
	Alias        string           `json:"alias"`
	URL          string           `json:"url"`
	Status       DeploymentStatus `json:"status"`
	ContainerID  string           `json:"container_id"`
	ImageID      string           `json:"image_id"`
	ProjectPath  string           `json:"project_path"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	DeployedAt   *time.Time       `json:"deployed_at"`
	Error        string           `json:"error"`
	HealthCheck  *HealthCheckInfo `json:"health_check"`
}

// DeploymentStatus represents the status of a deployment.
type DeploymentStatus string

const (
	DeploymentStatusReceived    DeploymentStatus = "received"
	DeploymentStatusExtracting  DeploymentStatus = "extracting"
	DeploymentStatusValidating  DeploymentStatus = "validating"
	DeploymentStatusBuilding    DeploymentStatus = "building"
	DeploymentStatusStarting    DeploymentStatus = "starting"
	DeploymentStatusHealthCheck DeploymentStatus = "health_check"
	DeploymentStatusRunning     DeploymentStatus = "running"
	DeploymentStatusUnhealthy   DeploymentStatus = "unhealthy"
	DeploymentStatusDeleting    DeploymentStatus = "deleting"
	DeploymentStatusDeleted     DeploymentStatus = "deleted"
	DeploymentStatusFailed      DeploymentStatus = "failed"
)

// HealthCheckInfo represents health check information for a deployment.
type HealthCheckInfo struct {
	Status        string    `json:"status"`
	CheckedAt     time.Time `json:"checked_at"`
	ResponseTimeMs int64     `json:"response_time_ms"`
	FailureCount  int       `json:"failure_count"`
	LastError     string    `json:"last_error"`
}

// ErrorResponse represents a generic error response from the API.
type ErrorResponse struct {
	Status string `json:"status"`
	Error  APIError `json:"error"`
}

// APIError represents detailed API error information.
type APIError struct {
	Code        string        `json:"code"`
	Message     string        `json:"message"`
	Details     []ValidationError `json:"details"`
	RequestID   string        `json:"request_id"`
	DeploymentID string        `json:"deployment_id"`
	Logs        string        `json:"logs"`
	Documentation string      `json:"documentation"`
}

// ValidationError represents a single validation error detail.
type ValidationError struct {
	Field     string `json:"field"`
	Error     string `json:"error"`
	Suggestion string `json:"suggestion"`
}

// ListApps makes an API call to list all deployed applications.
func ListApps(apiURL, apiToken string) (*DeploymentsListResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/deployments", apiURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", apiToken))
	req.Header.Add("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return nil, fmt.Errorf("API error (%s): %s - %s", errResp.Error.Code, errResp.Error.Message, string(body))
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var deployments DeploymentsListResponse
	if err := json.Unmarshal(body, &deployments); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	return &deployments, nil
}
