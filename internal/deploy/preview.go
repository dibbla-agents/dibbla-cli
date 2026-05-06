package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// PreviewService mirrors deploy-api PreviewService so consumers can decode
// a preview response without depending on the server package.
type PreviewService struct {
	Name        string            `json:"name"`
	IsBuilt     bool              `json:"is_built"`
	IsPublic    bool              `json:"is_public"`
	Image       string            `json:"image,omitempty"`
	Port        *int              `json:"port,omitempty"`
	Replicas    int               `json:"replicas"`
	CPU         string            `json:"cpu,omitempty"`
	Memory      string            `json:"memory,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
}

// PreviewSkippedSvc mirrors deploy-api PreviewSkippedSvc.
type PreviewSkippedSvc struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// PreviewError mirrors deploy-api PreviewError.
type PreviewError struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

// PreviewResponse mirrors deploy-api PreviewResponse.
type PreviewResponse struct {
	Valid           bool                `json:"valid"`
	Alias           string              `json:"alias"`
	Env             string              `json:"env"`
	ActiveServices  []PreviewService    `json:"active_services"`
	SkippedServices []PreviewSkippedSvc `json:"skipped_services,omitempty"`
	PublicService   string              `json:"public_service,omitempty"`
	Warnings        []string            `json:"warnings,omitempty"`
	Errors          []PreviewError      `json:"errors,omitempty"`
}

// PreviewOptions selects what to preview.
type PreviewOptions struct {
	APIURL    string
	APIToken  string
	Path      string
	Alias     string   // optional override for the directory-name alias
	TargetEnv string   // env block; defaults to "prod" server-side
	Profiles  []string // activated profiles
	NoPublic  bool     // allow worker-only deploys
	Port      string   // forwarded so the no-manifest synthesizer can echo a port
}

// Preview uploads the archive to /deployments/preview and returns the
// server's structured response. Local manifest validation runs first and
// short-circuits before any HTTP call.
//
// Network and decoding failures are returned as errors; validation failures
// from the server are inside resp.Errors with resp.Valid == false.
func Preview(opts PreviewOptions) (*PreviewResponse, error) {
	path := opts.Path
	if path == "" {
		path = "."
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if err := validateLocalManifest(absPath); err != nil {
		return nil, err
	}

	archive, err := createArchive(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}
	if len(archive) > 50*1024*1024 {
		return nil, fmt.Errorf("archive size (%d MB) exceeds 50 MB limit", len(archive)/(1024*1024))
	}

	alias := filepath.Base(absPath)
	if opts.Alias != "" {
		alias = opts.Alias
	}

	return uploadPreview(opts, archive, alias)
}

// uploadPreview is split out for tests (HTTP transport mocking via APIURL).
func uploadPreview(opts PreviewOptions, archive []byte, alias string) (*PreviewResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("archive", "app.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(archive); err != nil {
		return nil, fmt.Errorf("write archive: %w", err)
	}

	writeField := func(name, val string) {
		if val == "" {
			return
		}
		_ = writer.WriteField(name, val)
	}
	writeField("app_name", alias)
	writeField("env", opts.TargetEnv)
	writeField("port", opts.Port)
	if len(opts.Profiles) > 0 {
		profilesJSON, _ := json.Marshal(opts.Profiles)
		writeField("profiles", string(profilesJSON))
	}
	if opts.NoPublic {
		writeField("no_public", "true")
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	url := strings.TrimSuffix(opts.APIURL, "/") + "/api/deploy/deployments/preview"
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+opts.APIToken)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 200 with a possibly-invalid PreviewResponse OR 4xx with an ErrorResponse.
	if resp.StatusCode == http.StatusOK {
		var p PreviewResponse
		if err := json.Unmarshal(respBody, &p); err != nil {
			return nil, fmt.Errorf("decode preview response: %w (body=%s)", err, string(respBody))
		}
		return &p, nil
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(respBody, &errResp); err != nil {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil, formatAPIError(&errResp)
}
