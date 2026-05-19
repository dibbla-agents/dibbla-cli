// Package vcs is a thin client for the deploy-api Version Control endpoints.
package vcs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Info mirrors vcsInfoResponse on the server.
type Info struct {
	DefaultBranch        string  `json:"default_branch"`
	LatestSHA            string  `json:"latest_sha,omitempty"`
	LatestCommit         *Commit `json:"latest_commit,omitempty"`
	CommitCount          int     `json:"commit_count"`
	CloneURL             string  `json:"clone_url"`
	CloneURLWithEnvToken string  `json:"clone_url_with_env_token"`
	CLICommand           string  `json:"cli_command"`
	RunningSHA           string  `json:"running_sha,omitempty"`
}

// Commit mirrors gitlog.Commit.
type Commit struct {
	SHA         string    `json:"sha"`
	ShortSHA    string    `json:"short_sha"`
	AuthorName  string    `json:"author_name"`
	AuthorEmail string    `json:"author_email"`
	CommittedAt time.Time `json:"committed_at"`
	Subject     string    `json:"subject"`
	DeployID    string    `json:"deploy_id,omitempty"`
}

// APIError surfaces non-2xx responses from deploy-api.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// GetInfo fetches /api/deploy/deployments/<alias>/vcs/info.
func GetInfo(apiURL, apiToken, alias string) (*Info, error) {
	base := strings.TrimSuffix(apiURL, "/")
	url := fmt.Sprintf("%s/api/deploy/deployments/%s/vcs/info", base, alias)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &info, nil
}
