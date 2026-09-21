package apps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DeploymentsListResponse represents the API response for listing deployments.
type DeploymentsListResponse struct {
	Deployments []Deployment `json:"deployments"`
	Total       int          `json:"total"`
}

// Deployment represents a single application deployment.
type Deployment struct {
	ID              string           `json:"id"`
	Alias           string           `json:"alias"`
	URL             string           `json:"url"`
	Status          DeploymentStatus `json:"status"`
	ContainerID     string           `json:"container_id"`
	ImageID         string           `json:"image_id"`
	ProjectPath     string           `json:"project_path"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	DeployedAt      *time.Time       `json:"deployed_at"`
	Error           string           `json:"error"`
	HealthCheck     *HealthCheckInfo `json:"health_check"`
	RequireLogin    bool             `json:"require_login"`
	AppAccessPolicy string           `json:"app_access_policy,omitempty"`
	GoogleScopes    []string         `json:"google_scopes,omitempty"`
	MicrosoftScopes []string         `json:"microsoft_scopes,omitempty"`

	// The fields below are present on GET /deployments/{alias} responses and
	// rendered by `apps get`. They stay optional so the update response (which
	// carries fewer fields) parses into the same struct.
	Replicas      *int                `json:"replicas,omitempty"`
	CPU           string              `json:"cpu,omitempty"`
	Memory        string              `json:"memory,omitempty"`
	Description   string              `json:"description,omitempty"`
	ReviewStatus  string              `json:"review_status,omitempty"`
	ReviewSummary string              `json:"review_summary,omitempty"`
	ServiceCount  int                 `json:"service_count,omitempty"`
	Services      []DeploymentService `json:"services,omitempty"`
	// CommitSHA is the commit on the app's main branch the running revision
	// was built from (DIB-902). Empty for apps last deployed before the
	// server recorded it.
	CommitSHA string `json:"commit_sha,omitempty"`
	// ReviewBody is the REVIEW.md as deployed (GET only).
	ReviewBody string `json:"review_body,omitempty"`
	// Security is the app card's security section (DIB-965): the guardrails
	// review and which version it applies to, the build-time scan and when
	// its findings last changed, the maintenance agent's standing. GET only;
	// nil from a server that predates it.
	Security *Security `json:"security,omitempty"`
}

// Security mirrors deploy-api's `security` on GET /deployments/{alias}.
type Security struct {
	Review      *SecurityReview      `json:"review,omitempty"`
	Scan        *SecurityScan        `json:"scan,omitempty"`
	Maintenance *SecurityMaintenance `json:"maintenance,omitempty"`
}

// SecurityReview is the pre-deploy guardrails review as it applies to the
// running revision.
type SecurityReview struct {
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Present is false when the app never carried a REVIEW.md.
	Present bool `json:"present"`
	// ReviewedAt is when this exact review was first deployed and CommitSHA
	// the commit it was written for; both absent for reviews the server only
	// knows from the workload.
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	CommitSHA  string     `json:"commit_sha,omitempty"`
	// CodeChangedSince: the running commit is not the reviewed one.
	CodeChangedSince bool `json:"code_changed_since"`
}

// SecuritySeverityCounts is the per-severity tally of vulnerability findings.
type SecuritySeverityCounts struct {
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Medium     int `json:"medium"`
	Low        int `json:"low"`
	Negligible int `json:"negligible"`
	Unknown    int `json:"unknown"`
	Total      int `json:"total"`
}

// SecurityScan is the build-time scan of the running revision.
type SecurityScan struct {
	// Status is running | completed | partial | failed.
	Status          string                 `json:"status"`
	DeploymentID    string                 `json:"deployment_id"`
	CommitSHA       string                 `json:"commit_sha,omitempty"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	Vulnerabilities SecuritySeverityCounts `json:"vulnerabilities"`
	Secrets         int                    `json:"secrets"`
	Images          int                    `json:"images"`
	Packages        int                    `json:"packages"`
	Errors          []string               `json:"errors,omitempty"`
	// FindingsChangedAt is when the app's finding SET last changed, carried
	// forward across revisions that raised the same findings.
	FindingsChangedAt *time.Time `json:"findings_changed_at,omitempty"`
}

// SecurityMaintenance is the maintenance agent's standing for the app.
type SecurityMaintenance struct {
	// Configured: the organization has the agent on at all. Enabled: the
	// effective per-app switch.
	Configured    bool       `json:"configured"`
	Enabled       bool       `json:"enabled"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus string     `json:"last_run_status,omitempty"`
	LastRunCode   string     `json:"last_run_code,omitempty"`
	// LastChangeAt is the latest of the running revision's rollout and the
	// finding set changing; ChangedSinceLastRun whether that is newer than
	// the last run.
	LastChangeAt        *time.Time `json:"last_change_at,omitempty"`
	ChangedSinceLastRun bool       `json:"changed_since_last_run"`
	PendingProposals    int        `json:"pending_proposals"`
}

// DeploymentService is one service of a multi-service deployment, as returned
// by GET /deployments/{alias}.
type DeploymentService struct {
	Name          string `json:"name"`
	Image         string `json:"image,omitempty"`
	Port          *int   `json:"port,omitempty"`
	Replicas      int    `json:"replicas"`
	ReadyReplicas int    `json:"ready_replicas"`
	CPU           string `json:"cpu,omitempty"`
	Memory        string `json:"memory,omitempty"`
	IsPublic      bool   `json:"is_public"`
	IsBuilt       bool   `json:"is_built"`
	Status        string `json:"status,omitempty"`
	Stateful      bool   `json:"stateful,omitempty"`
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
	Status         string    `json:"status"`
	CheckedAt      time.Time `json:"checked_at"`
	ResponseTimeMs int64     `json:"response_time_ms"`
	FailureCount   int       `json:"failure_count"`
	LastError      string    `json:"last_error"`
}

// ErrorResponse represents a generic error response from the API.
type ErrorResponse struct {
	Status string   `json:"status"`
	Error  APIError `json:"error"`
}

// APIError represents detailed API error information.
type APIError struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Details       []ValidationError `json:"details"`
	RequestID     string            `json:"request_id"`
	DeploymentID  string            `json:"deployment_id"`
	Logs          string            `json:"logs"`
	Documentation string            `json:"documentation"`
}

// ValidationError represents a single validation error detail.
type ValidationError struct {
	Field      string `json:"field"`
	Error      string `json:"error"`
	Suggestion string `json:"suggestion"`
}

// DeleteResponse represents the API response for deleting a deployment.
type DeleteResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// UpdateDeploymentRequest is the request body for PUT /deployments/{alias}.
type UpdateDeploymentRequest struct {
	EnvironmentVariables map[string]string `json:"environment_variables,omitempty"`
	Replicas             *int32            `json:"replicas,omitempty"`
	CPU                  string            `json:"cpu,omitempty"`
	Memory               string            `json:"memory,omitempty"`
	Port                 *int              `json:"port,omitempty"`
	FaviconURL           *string           `json:"favicon_url,omitempty"`
	RequireLogin         *bool             `json:"require_login,omitempty"`
	AppAccessPolicy      *string           `json:"app_access_policy,omitempty"`
	GoogleScopes         []string          `json:"google_scopes,omitempty"`
	MicrosoftScopes      []string          `json:"microsoft_scopes,omitempty"`
}

// ListApps makes an API call to list all deployed applications.
func ListApps(apiURL, apiToken string) (*DeploymentsListResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL = strings.TrimSuffix(apiURL, "/")
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/deploy/deployments", apiURL), nil)
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

// DeleteApp makes an API call to delete a specific application by alias.
func DeleteApp(apiURL, apiToken, alias string) (*DeleteResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL = strings.TrimSuffix(apiURL, "/")
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/deploy/deployments/%s", apiURL, alias), nil)
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

	var deleteResponse DeleteResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	return &deleteResponse, nil
}

// UpdateApp updates an existing deployment by alias (PUT /deployments/{alias}).
func UpdateApp(apiURL, apiToken, alias string, req UpdateDeploymentRequest) (*Deployment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	apiURL = strings.TrimSuffix(apiURL, "/")
	httpReq, err := http.NewRequest("PUT", fmt.Sprintf("%s/api/deploy/deployments/%s", apiURL, alias), strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("API error (%s): %s", errResp.Error.Code, errResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var deployment Deployment
	if err := json.Unmarshal(respBody, &deployment); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}
	return &deployment, nil
}
