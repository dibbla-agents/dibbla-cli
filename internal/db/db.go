package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

const requestTimeout = 60 * time.Second

// DatabasesListResponse is the response for listing databases.
type DatabasesListResponse struct {
	Databases []string `json:"databases"`
	Total     int     `json:"total"`
}

// DatabaseCreateResponse is the response for creating a database.
type DatabaseCreateResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Database string `json:"database"`
}

// DatabaseRestoreResponse is the response for restoring a database.
type DatabaseRestoreResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Database string `json:"database"`
}

// DeleteResponse is the response for deleting a database.
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

func makeAPIURL(base, path string) string {
	return strings.TrimSuffix(base, "/") + path
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

// ListDatabases returns all managed databases.
func ListDatabases(apiURL, apiToken string) (*DatabasesListResponse, error) {
	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("GET", makeAPIURL(apiURL, "/databases"), nil)
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

	var out DatabasesListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// CreateDatabase creates a new managed database.
func CreateDatabase(apiURL, apiToken, name string) (*DatabaseCreateResponse, error) {
	client := &http.Client{Timeout: requestTimeout}
	payload, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequest("POST", makeAPIURL(apiURL, "/databases"), bytes.NewReader(payload))
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

	var out DatabaseCreateResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// DeleteDatabase deletes a database by name.
func DeleteDatabase(apiURL, apiToken, name string) (*DeleteResponse, error) {
	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("DELETE", makeAPIURL(apiURL, "/databases/"+name), nil)
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

// RestoreDatabase restores a database from an uploaded dump file.
func RestoreDatabase(apiURL, apiToken, name, dumpPath string) (*DatabaseRestoreResponse, error) {
	f, err := os.Open(dumpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open dump file: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	part, err := w.CreateFormFile("dump", "dump")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("failed to write dump to form: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest("POST", makeAPIURL(apiURL, "/databases/"+name+"/restore"), &body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(respBody, resp.StatusCode)
	}

	var out DatabaseRestoreResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, nil
}

// DumpDatabase downloads a database dump and writes it to out. Caller closes out.
func DumpDatabase(apiURL, apiToken, name string, out io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", makeAPIURL(apiURL, "/databases/"+name+"/dump"), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return parseError(body, resp.StatusCode)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}
