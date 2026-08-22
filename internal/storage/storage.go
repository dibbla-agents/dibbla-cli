// Package storage is the HTTP client for the managed object storage API
// (P-0026): buckets provisioned and operated like databases, with scoped
// credentials injected as STORAGE_<NAME>_* secrets.
package storage

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

const requestTimeout = 60 * time.Second

// BucketsListResponse is the response for listing buckets.
type BucketsListResponse struct {
	Buckets []string `json:"buckets"`
	Total   int      `json:"total"`
}

// BucketCreateResponse is the response for creating a bucket.
type BucketCreateResponse struct {
	Status      string   `json:"status"`
	Message     string   `json:"message"`
	Bucket      string   `json:"bucket"`
	Endpoint    string   `json:"endpoint"`
	QuotaBytes  int64    `json:"quota_bytes"`
	SecretNames []string `json:"secret_names"`
}

// BucketUsage describes one bucket's usage vs quota.
type BucketUsage struct {
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes"`
	Objects    int64  `json:"objects"`
	QuotaBytes int64  `json:"quota_bytes"`
}

// BucketsInfoResponse is the response for bucket usage info.
type BucketsInfoResponse struct {
	Buckets []BucketUsage `json:"buckets"`
	Total   int           `json:"total"`
}

// BucketRotateResponse is the response for rotating bucket credentials.
type BucketRotateResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	Bucket    string `json:"bucket"`
	Restarted bool   `json:"restarted"`
}

// BucketDeleteResponse is the response for deleting a bucket.
type BucketDeleteResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Bucket  string `json:"bucket"`
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
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Code != "" {
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
		if errResp.Error.Documentation != "" {
			msg = strings.TrimSuffix(msg, "\n") + "\nDocs: " + errResp.Error.Documentation
		}
		return fmt.Errorf("%s", strings.TrimSuffix(msg, "\n"))
	}
	return fmt.Errorf("API request failed with status %d: %s", statusCode, string(body))
}

func doJSON(method, url, token string, payload any, wantStatus int, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != wantStatus {
		return parseError(respBody, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}
	return nil
}

// ListBuckets returns all managed buckets.
func ListBuckets(apiURL, apiToken string) (*BucketsListResponse, error) {
	var out BucketsListResponse
	if err := doJSON("GET", makeAPIURL(apiURL, "/api/deploy/buckets"), apiToken, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateBucket creates a managed bucket. deploymentAlias, size and expireDays
// are optional (size e.g. "5Gi"; empty means the server default).
func CreateBucket(apiURL, apiToken, name, deploymentAlias, size string, expireDays int) (*BucketCreateResponse, error) {
	reqBody := map[string]any{"name": name}
	if deploymentAlias != "" {
		reqBody["deployment_alias"] = deploymentAlias
	}
	if size != "" {
		reqBody["size"] = size
	}
	if expireDays > 0 {
		reqBody["expire_days"] = expireDays
	}
	var out BucketCreateResponse
	if err := doJSON("POST", makeAPIURL(apiURL, "/api/deploy/buckets"), apiToken, reqBody, http.StatusCreated, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBucket deletes a bucket. force deletes it with its contents.
func DeleteBucket(apiURL, apiToken, name string, force bool) (*BucketDeleteResponse, error) {
	u := makeAPIURL(apiURL, "/api/deploy/buckets/"+url.PathEscape(name))
	if force {
		u += "?force=true"
	}
	var out BucketDeleteResponse
	if err := doJSON("DELETE", u, apiToken, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateBucket re-mints the bucket's scoped credentials. Unless noRestart is
// set, the bound deployment's services are restarted so pods pick up the new
// key (envFrom values only change on restart).
func RotateBucket(apiURL, apiToken, name string, noRestart bool) (*BucketRotateResponse, error) {
	u := makeAPIURL(apiURL, "/api/deploy/buckets/"+url.PathEscape(name)+"/rotate")
	if noRestart {
		u += "?no_restart=true"
	}
	var out BucketRotateResponse
	if err := doJSON("POST", u, apiToken, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BucketsInfo returns usage vs quota for every bucket.
func BucketsInfo(apiURL, apiToken string) (*BucketsInfoResponse, error) {
	var out BucketsInfoResponse
	if err := doJSON("GET", makeAPIURL(apiURL, "/api/deploy/buckets/info"), apiToken, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnvName maps a bucket name to the <NAME> segment of its STORAGE_<NAME>_*
// secrets: uppercased, hyphens to underscores — mirrors the server transform.
func EnvName(bucket string) string {
	return strings.ToUpper(strings.ReplaceAll(bucket, "-", "_"))
}

// FormatBytes renders a byte count in Gi/Mi/Ki with one decimal.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMi", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKi", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
