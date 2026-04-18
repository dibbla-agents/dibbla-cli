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
	Logs          string            `json:"logs,omitempty"`
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
	Update   bool   // Rolling update with zero downtime (mutually exclusive with Force)
	Alias    string // Custom alias; when empty, derived from directory name
	// Optional deploy API params
	Env        []string // KEY=value pairs (Docker-style), e.g. NODE_ENV=production
	CPU        string   // e.g. 500m
	Memory     string   // e.g. 512Mi
	Port       string   // e.g. 3000
	FaviconURL string   // e.g. https://example.com/favicon.ico
	// Login guard settings
	RequireLogin bool     // Require authentication to access the app
	AccessPolicy string   // "all_members" or "invite_only"
	GoogleScopes []string // Google OAuth scopes to request
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

	// Get app name from alias or path
	appName := filepath.Base(absPath)
	if opts.Alias != "" {
		appName = opts.Alias
	}

	// Upload to API
	return upload(opts.APIURL, opts.APIToken, archive, appName, opts.Force, opts.Update, opts.Env, opts.CPU, opts.Memory, opts.Port, opts.FaviconURL, opts.RequireLogin, opts.AccessPolicy, opts.GoogleScopes)
}

// createArchive creates a tar.gz archive from the given directory.
//
// Symlink handling: a symlink whose resolved target is inside the archive root
// is dereferenced — its content (file or directory tree) is emitted as regular
// tar entries under the symlink's path. A symlink whose target escapes the
// archive root (including absolute symlinks such as /etc/passwd) is skipped
// entirely, never written to the archive. This prevents accidental packaging
// of host files and also avoids tripping the backend's archive-safety check,
// which rejects any symlink target containing "..".
func createArchive(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	rootAbs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Resolve symlinks in the archive root itself so containment checks against
	// EvalSymlinks-resolved targets are on the same footing. Important on macOS
	// where /var is a symlink to /private/var.
	if resolved, rerr := filepath.EvalSymlinks(rootAbs); rerr == nil {
		rootAbs = resolved
	}

	var skipped []string

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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

		// Symlink: dereference-if-within-root, skip-if-outside.
		if info.Mode()&os.ModeSymlink != 0 {
			visited := make(map[string]bool)
			didSkip, serr := archiveSymlink(tw, path, relPath, rootAbs, visited)
			if serr != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping symlink %s: %v\n", relPath, serr)
				return nil
			}
			if didSkip {
				skipped = append(skipped, relPath)
			}
			return nil
		}

		// Regular file or directory: write a standard header and content.
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use relative path in archive with POSIX separators.
		// filepath.Rel returns OS-native separators (backslashes on Windows),
		// but tar archives must use forward slashes for Linux/BuildKit compatibility.
		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

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

	if walkErr != nil {
		return nil, walkErr
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	if err := gzw.Close(); err != nil {
		return nil, err
	}

	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d symlink(s) pointing outside the deploy root: %s\n",
			len(skipped), strings.Join(skipped, ", "))
	}

	return buf.Bytes(), nil
}

// archiveSymlink handles a single symlink encountered during the walk.
// Returns skipped=true when nothing was written (target escaped root, broken
// link, unsupported mode) so the caller can count it for the summary line.
// A visited map is threaded through recursion so a self-referential directory
// loop terminates; sibling symlinks to the same target each get a fresh map
// from the top-level walker and are not de-duplicated across independent
// dereference chains.
func archiveSymlink(tw *tar.Writer, path, logicalPath, rootAbs string, visited map[string]bool) (skipped bool, err error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Broken, dangling, or cycle detected by Go's resolver — skip quietly.
		return true, nil
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return true, err
	}
	if !isWithinRoot(targetAbs, rootAbs) {
		return true, nil
	}
	if visited[targetAbs] {
		// Already expanded in this dereference chain — prevents self-loop
		// recursion. Not counted as "skipped" because the content is already
		// in the archive under a parent logical path.
		return false, nil
	}
	visited[targetAbs] = true

	targetInfo, err := os.Stat(target)
	if err != nil {
		return true, nil
	}

	if targetInfo.Mode().IsRegular() {
		return false, writeSymlinkedFile(tw, targetAbs, targetInfo, logicalPath)
	}
	if targetInfo.IsDir() {
		return false, archiveSymlinkedDir(tw, targetAbs, logicalPath, rootAbs, visited)
	}
	// Sockets, devices, named pipes, etc.
	return true, nil
}

// writeSymlinkedFile emits a single regular-file tar entry at logicalPath,
// containing the content and mode of the resolved target file.
func writeSymlinkedFile(tw *tar.Writer, targetAbs string, targetInfo os.FileInfo, logicalPath string) error {
	header, err := tar.FileInfoHeader(targetInfo, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(logicalPath)
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(targetAbs)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(tw, f); err != nil {
		return err
	}
	return nil
}

// archiveSymlinkedDir walks a directory reached through a symlink and emits
// tar entries under logicalPrefix. Sub-entries pass through shouldExclude and
// the same in-root check; sub-symlinks recurse via archiveSymlink so an
// escaping or cyclic link inside a dereferenced tree is handled safely.
func archiveSymlinkedDir(tw *tar.Writer, realRoot, logicalPrefix, archiveRootAbs string, visited map[string]bool) error {
	topInfo, err := os.Stat(realRoot)
	if err != nil {
		return err
	}
	topHeader, err := tar.FileInfoHeader(topInfo, "")
	if err != nil {
		return err
	}
	topHeader.Name = filepath.ToSlash(logicalPrefix) + "/"
	if err := tw.WriteHeader(topHeader); err != nil {
		return err
	}

	return filepath.Walk(realRoot, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(realRoot, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil // top dir already emitted above
		}

		logical := filepath.Join(logicalPrefix, rel)

		if shouldExclude(logical, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if _, serr := archiveSymlink(tw, p, logical, archiveRootAbs, visited); serr != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping symlink %s: %v\n", logical, serr)
			}
			return nil
		}

		header, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		header.Name = filepath.ToSlash(logical)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, oerr := os.Open(p)
			if oerr != nil {
				return oerr
			}
			defer f.Close()
			if _, cerr := io.Copy(tw, f); cerr != nil {
				return cerr
			}
		}
		return nil
	})
}

// isWithinRoot reports whether targetAbs is equal to or a descendant of rootAbs.
// Both paths must already be absolute and cleaned.
func isWithinRoot(targetAbs, rootAbs string) bool {
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	if filepath.IsAbs(rel) {
		return false
	}
	return true
}

// shouldExclude checks if a path should be excluded from the archive
func shouldExclude(relPath string, info os.FileInfo) bool {
	// Check excluded paths
	baseName := filepath.Base(relPath)
	for _, excluded := range excludedPaths {
		if baseName == excluded || strings.HasPrefix(filepath.ToSlash(relPath), excluded+"/") {
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
func upload(apiURL, apiToken string, archive []byte, appName string, force, update bool, envPairs []string, cpu, memory, port, faviconURL string, requireLogin bool, accessPolicy string, googleScopes []string) (*DeployResponse, error) {
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

	// Add update field if set (rolling update)
	if update {
		if err := writer.WriteField("update", "true"); err != nil {
			return nil, fmt.Errorf("failed to write update field: %w", err)
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
	if faviconURL != "" {
		if err := writer.WriteField("favicon_url", faviconURL); err != nil {
			return nil, fmt.Errorf("failed to write favicon_url field: %w", err)
		}
	}
	if requireLogin {
		if err := writer.WriteField("require_login", "true"); err != nil {
			return nil, fmt.Errorf("failed to write require_login field: %w", err)
		}
	}
	if accessPolicy != "" {
		if err := writer.WriteField("app_access_policy", accessPolicy); err != nil {
			return nil, fmt.Errorf("failed to write app_access_policy field: %w", err)
		}
	}
	if len(googleScopes) > 0 {
		scopesJSON, _ := json.Marshal(googleScopes)
		if err := writer.WriteField("google_scopes", string(scopesJSON)); err != nil {
			return nil, fmt.Errorf("failed to write google_scopes field: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	url := strings.TrimSuffix(apiURL, "/") + "/api/deploy/deployments"
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

	if errResp.Error.Logs != "" {
		msg += "\n\nBuild logs:\n" + errResp.Error.Logs
	}

	return fmt.Errorf("%s", msg)
}
