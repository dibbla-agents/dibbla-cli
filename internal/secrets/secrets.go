package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const requestTimeout = 30 * time.Second

// SecretsListResponse is the response for listing secrets.
type SecretsListResponse struct {
	Secrets []SecretListItem `json:"secrets"`
	Total   int              `json:"total"`
}

// SecretListItem is a secret in a list (no value).
type SecretListItem struct {
	Name             string `json:"name"`
	DeploymentAlias  string `json:"deployment_alias"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// SecretResponse is a full secret (includes value when getting one).
type SecretResponse struct {
	Name             string `json:"name"`
	Value            string `json:"value,omitempty"`
	DeploymentAlias  string `json:"deployment_alias"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// SecretCreateResponse is the response for creating a secret.
type SecretCreateResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Secret  SecretResponse `json:"secret"`
}

// DeleteResponse is the response for deleting a secret.
type DeleteResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ErrorResponse represents an error response from the API.
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
	Documentation string            `json:"documentation"`
}

// ValidationError represents a single validation error detail.
type ValidationError struct {
	Field      string `json:"field"`
	Error      string `json:"error"`
	Suggestion string `json:"suggestion"`
}

func makeAPIURL(base, path string, query url.Values) string {
	u := strings.TrimSuffix(base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func parseError(body []byte, statusCode int) error {
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil {
		msg := fmt.Sprintf("%s: %s", errResp.Error.Code, errResp.Error.Message)
		if len(errResp.Error.Details) > 0 {
			msg += "\n"
			for _, d := range errResp.Error.Details {
				msg += fmt.Sprintf("  - %s: %s", d.Field, d.Error)
				if d.Suggestion != "" {
					msg += fmt.Sprintf(" (%s)", d.Suggestion)
				}
				msg += "\n"
			}
		}
		return fmt.Errorf("%s", strings.TrimSuffix(msg, "\n"))
	}
	return fmt.Errorf("API request failed with status %d: %s", statusCode, string(body))
}

// ListSecrets returns secrets for a scope. If deployment is empty, returns global secrets only.
func ListSecrets(apiURL, apiToken, deployment string) (*SecretsListResponse, error) {
	query := url.Values{}
	if deployment != "" {
		query.Set("deployment", deployment)
	}
	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("GET", makeAPIURL(apiURL, "/secrets", query), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(body, resp.StatusCode)
	}

	var out SecretsListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// CreateSecret creates or updates a secret. deploymentAlias can be empty for a global secret.
func CreateSecret(apiURL, apiToken, name, value, deploymentAlias string) (*SecretCreateResponse, error) {
	payload := map[string]string{"name": name, "value": value}
	if deploymentAlias != "" {
		payload["deployment_alias"] = deploymentAlias
	}
	raw, _ := json.Marshal(payload)

	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("POST", makeAPIURL(apiURL, "/secrets", nil), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, parseError(body, resp.StatusCode)
	}

	var out SecretCreateResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// GetSecret returns a secret by name. deployment can be empty for a global secret.
func GetSecret(apiURL, apiToken, name, deployment string) (*SecretResponse, error) {
	query := url.Values{}
	if deployment != "" {
		query.Set("deployment", deployment)
	}
	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("GET", makeAPIURL(apiURL, "/secrets/"+url.PathEscape(name), query), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(body, resp.StatusCode)
	}

	var out SecretResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// DeleteSecret deletes a secret by name. deployment can be empty for a global secret.
func DeleteSecret(apiURL, apiToken, name, deployment string) (*DeleteResponse, error) {
	query := url.Values{}
	if deployment != "" {
		query.Set("deployment", deployment)
	}
	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("DELETE", makeAPIURL(apiURL, "/secrets/"+url.PathEscape(name), query), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(body, resp.StatusCode)
	}

	var out DeleteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}
