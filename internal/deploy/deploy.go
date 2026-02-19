package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeployResponse represents a successful deployment response
type DeployResponse struct {
	Status     string     `json:"status"`
	Deployment Deployment `json:"deployment"`
}

// Deployment contains deployment details
type Deployment struct {
	ID          string       `json:"id"`
	Alias       string       `json:"alias"`
	URL         string       `json:"url"`
	Status      string       `json:"status"`
	ContainerID string       `json:"container_id"`
	ImageID     string       `json:"image_id"`
	CreatedAt   string       `json:"created_at"`
	DeployedAt  string       `json:"deployed_at"`
	HealthCheck *HealthCheck `json:"health_check,omitempty"`
}

// HealthCheck contains health check details
type HealthCheck struct {
	Status         string `json:"status"`
	CheckedAt      string `json:"checked_at"`
	ResponseTimeMs int    `json:"response_time_ms"`
}

// ErrorResponse represents an error response from the API
type ErrorResponse struct {
	Status string      `json:"status"`
	Error  ErrorDetail `json:"error"`
}

// ErrorDetail contains error details
type ErrorDetail struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Details       []ValidationError `json:"details,omitempty"`
	RequestID     string            `json:"request_id,omitempty"`
	Documentation string            `json:"documentation,omitempty"`
}

// ValidationError represents a validation error detail
type ValidationError struct {
	Field      string `json:"field"`
	Error      string `json:"error"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Options configures the deployment
type Options struct {
	APIURL   string
	APIToken string
	Path     string
	Force    bool
	// Optional deploy API params
	Env    []string // KEY=value pairs (Docker-style), e.g. NODE_ENV=production
	CPU    string   // e.g. 500m
	Memory string   // e.g. 512Mi
	Port   string   // e.g. 3000
}

// excludedPaths are paths that should not be included in the archive
var excludedPaths = []string{
	".git",
	"node_modules",
	".env.production",
	".env.prod",
	"id_rsa",
	"id_ed25519",
	"id_ecdsa",
	"id_dsa",
	"credentials.json",
	"service-account.json",
}

// excludedExtensions are file extensions that should not be included
var excludedExtensions = []string{
	".pem",
	".key",
	".exe",
	".dll",
	".so",
	".dylib",
	".bat",
	".cmd",
	".com",
	".msi",
	".scr",
	".pif",
}

// Run executes the deployment
func Run(opts Options) (*DeployResponse, error) {
	// Validate path
	path := opts.Path
	if path == "" {
		path = "."
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Create archive
	archive, err := createArchive(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	// Check archive size (50MB limit)
	if len(archive) > 50*1024*1024 {
		return nil, fmt.Errorf("archive size (%d MB) exceeds 50 MB limit", len(archive)/(1024*1024))
	}

	// Get app name from path
	appName := filepath.Base(absPath)

	// Upload to API
	return upload(opts.APIURL, opts.APIToken, archive, appName, opts.Force, opts.Env, opts.CPU, opts.Memory, opts.Port)
}

// createArchive creates a tar.gz archive from the given directory
func createArchive(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		// Check if path should be excluded
		if shouldExclude(relPath, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use relative path in archive
		header.Name = relPath

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			header.Linkname = link
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content for regular files
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := gzw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// shouldExclude checks if a path should be excluded from the archive
func shouldExclude(relPath string, info os.FileInfo) bool {
	// Check excluded paths
	baseName := filepath.Base(relPath)
	for _, excluded := range excludedPaths {
		if baseName == excluded || strings.HasPrefix(relPath, excluded+string(os.PathSeparator)) {
			return true
		}
	}

	// Check excluded extensions
	ext := strings.ToLower(filepath.Ext(relPath))
	for _, excluded := range excludedExtensions {
		if ext == excluded {
			return true
		}
	}

	return false
}

// envPairsToJSON converts Docker-style KEY=value pairs into a JSON object string for the API.
// Splits on the first "=" so values may contain "=".
func envPairsToJSON(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		idx := strings.Index(p, "=")
		if idx <= 0 {
			continue
		}
		m[p[:idx]] = p[idx+1:]
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// upload sends the archive to the API
func upload(apiURL, apiToken string, archive []byte, appName string, force bool, envPairs []string, cpu, memory, port string) (*DeployResponse, error) {
	// Create multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add archive field
	part, err := writer.CreateFormFile("archive", "app.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := part.Write(archive); err != nil {
		return nil, fmt.Errorf("failed to write archive: %w", err)
	}

	// Add force field if set
	if force {
		if err := writer.WriteField("force", "true"); err != nil {
			return nil, fmt.Errorf("failed to write force field: %w", err)
		}
	}

	if appName != "" {
		if err := writer.WriteField("app_name", appName); err != nil {
			return nil, fmt.Errorf("failed to write app name field: %w", err)
		}
	}

	if envJSON := envPairsToJSON(envPairs); envJSON != "" {
		if err := writer.WriteField("env_vars", envJSON); err != nil {
			return nil, fmt.Errorf("failed to write env_vars field: %w", err)
		}
	}
	if cpu != "" {
		if err := writer.WriteField("cpu", cpu); err != nil {
			return nil, fmt.Errorf("failed to write cpu field: %w", err)
		}
	}
	if memory != "" {
		if err := writer.WriteField("memory", memory); err != nil {
			return nil, fmt.Errorf("failed to write memory field: %w", err)
		}
	}
	if port != "" {
		if err := writer.WriteField("port", port); err != nil {
			return nil, fmt.Errorf("failed to write port field: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	url := strings.TrimSuffix(apiURL, "/") + "/deployments"
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiToken)

	// Send request with timeout
	client := &http.Client{
		Timeout: 10 * time.Minute, // Long timeout for builds
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle success
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		var deployResp DeployResponse
		if err := json.Unmarshal(respBody, &deployResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		return &deployResp, nil
	}

	// Handle error
	var errResp ErrorResponse
	if err := json.Unmarshal(respBody, &errResp); err != nil {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil, formatAPIError(&errResp)
}

// formatAPIError creates a user-friendly error message from the API error response
func formatAPIError(errResp *ErrorResponse) error {
	msg := fmt.Sprintf("%s: %s", errResp.Error.Code, errResp.Error.Message)

	// Add validation details if present
	if len(errResp.Error.Details) > 0 {
		msg += "\n\nDetails:"
		for _, detail := range errResp.Error.Details {
			msg += fmt.Sprintf("\n  - %s: %s", detail.Field, detail.Error)
			if detail.Suggestion != "" {
				msg += fmt.Sprintf("\n    Suggestion: %s", detail.Suggestion)
			}
		}
	}

	return fmt.Errorf("%s", msg)
}
