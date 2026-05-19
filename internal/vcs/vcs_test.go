package vcs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetInfoSuccess(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"default_branch":"main",
			"latest_sha":"abc123",
			"commit_count":3,
			"clone_url":"https://git.dibbla.com/org/app.git",
			"cli_command":"dibbla clone org/app",
			"latest_commit":{"sha":"abc123","short_sha":"abc1234","author_name":"Jane","author_email":"j@x.io","committed_at":"2026-04-21T10:00:00Z","subject":"Deploy dep-3","deploy_id":"dep-3"}
		}`))
	}))
	defer srv.Close()

	info, err := GetInfo(srv.URL, "tok-xyz", "my-app")
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("auth header: got %q want %q", gotAuth, "Bearer tok-xyz")
	}
	if gotPath != "/api/deploy/deployments/my-app/vcs/info" {
		t.Errorf("path: got %q", gotPath)
	}
	if info.LatestSHA != "abc123" {
		t.Errorf("latest sha: got %q", info.LatestSHA)
	}
	if info.LatestCommit == nil || info.LatestCommit.DeployID != "dep-3" {
		t.Errorf("latest commit deploy id missing")
	}
}

func TestGetInfoPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "app not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := GetInfo(srv.URL, "tok", "missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d want 404", apiErr.StatusCode)
	}
}
