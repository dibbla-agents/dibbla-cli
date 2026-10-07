// Package storage is the HTTP client for the managed object storage API
// (P-0026): buckets provisioned and operated like databases, with scoped
// credentials injected as STORAGE_<NAME>_* secrets.
package storage

import (
	"bytes"
	"encoding/json"
	"errors"
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
	Name            string `json:"name"`
	DeploymentAlias string `json:"deployment_alias,omitempty"`
	SizeBytes       int64  `json:"size_bytes"`
	Objects         int64  `json:"objects"`
	QuotaBytes      int64  `json:"quota_bytes"`
}

// Object is one stored object as listed by GET /buckets/{name}/objects.
type Object struct {
	Key          string    `json:"key"`
	SizeBytes    int64     `json:"size_bytes"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified"`
}

// ObjectsPage is one page of a bucket listing (DIB-979).
type ObjectsPage struct {
	Bucket         string   `json:"bucket"`
	Objects        []Object `json:"objects"`
	Truncated      bool     `json:"truncated"`
	NextStartAfter string   `json:"next_start_after,omitempty"`
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

// BucketCredentials is a person's own short-lived key for one bucket
// (POST /buckets/{name}/credentials, DIB-1344). It is minted for the caller,
// works on that bucket's objects only and expires by itself — it is not the
// app's key, which lives in the app's write-only STORAGE_<NAME>_* secrets.
type BucketCredentials struct {
	Endpoint        string    `json:"endpoint"`
	Bucket          string    `json:"bucket"`
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// ErrBucketKeysUnsupported is returned when the server has no route for a
// person's bucket key: a Dibbla install older than DIB-1344.
var ErrBucketKeysUnsupported = errors.New("this Dibbla server does not issue personal bucket keys yet — ask your platform admin to update it")

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
	if statusCode == http.StatusNotFound {
		return &unroutedError{body: string(body)}
	}
	return fmt.Errorf("API request failed with status %d: %s", statusCode, string(body))
}

// unroutedError is a 404 without the API's error envelope: the server has no
// such route, as opposed to a route answering that a bucket does not exist.
type unroutedError struct{ body string }

func (e *unroutedError) Error() string {
	return fmt.Sprintf("API request failed with status %d: %s", http.StatusNotFound, e.body)
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

// IssueBucketCredentials asks for a key of the caller's own for one bucket,
// valid for an hour. The API token is the caller's identity; the key that
// comes back is theirs, not the app's.
func IssueBucketCredentials(apiURL, apiToken, name string) (*BucketCredentials, error) {
	u := makeAPIURL(apiURL, "/api/deploy/buckets/"+url.PathEscape(name)+"/credentials")
	var out BucketCredentials
	err := doJSON("POST", u, apiToken, nil, http.StatusOK, &out)
	var unrouted *unroutedError
	if errors.As(err, &unrouted) {
		return nil, ErrBucketKeysUnsupported
	}
	if err != nil {
		return nil, err
	}
	if out.AccessKeyID == "" || out.SecretAccessKey == "" {
		return nil, fmt.Errorf("the server answered without a key")
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

// ListObjects returns one page of the bucket's objects, recursively, lexically
// after startAfter. Follow Truncated/NextStartAfter for the rest.
func ListObjects(apiURL, apiToken, name, prefix, startAfter string, max int) (*ObjectsPage, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if startAfter != "" {
		q.Set("start_after", startAfter)
	}
	if max > 0 {
		q.Set("max", fmt.Sprint(max))
	}
	u := makeAPIURL(apiURL, "/api/deploy/buckets/"+url.PathEscape(name)+"/objects")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var out ObjectsPage
	if err := doJSON("GET", u, apiToken, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadObject streams one object into w. Caller owns w.
func DownloadObject(apiURL, apiToken, name, key string, w io.Writer) (int64, error) {
	// Keys may contain "/" — escape each segment so the path stays a path.
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	u := makeAPIURL(apiURL, "/api/deploy/buckets/"+url.PathEscape(name)+"/objects/"+strings.Join(segs, "/"))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, parseError(body, resp.StatusCode)
	}
	return io.Copy(w, resp.Body)
}
