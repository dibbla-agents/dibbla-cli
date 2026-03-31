package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGenerateState(t *testing.T) {
	state1, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error: %v", err)
	}
	if len(state1) == 0 {
		t.Fatal("GenerateState() returned empty string")
	}

	state2, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error: %v", err)
	}
	if state1 == state2 {
		t.Fatal("GenerateState() returned the same value twice")
	}
}

func TestBuildLoginURL(t *testing.T) {
	loginURL := BuildLoginURL("https://app.dibbla.com", 12345, "test-state")

	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("BuildLoginURL() returned invalid URL: %v", err)
	}

	if parsed.Host != "app.dibbla.com" {
		t.Errorf("host = %q, want %q", parsed.Host, "app.dibbla.com")
	}
	if parsed.Path != "/login" {
		t.Errorf("path = %q, want %q", parsed.Path, "/login")
	}

	returnTo := parsed.Query().Get("return_to")
	if returnTo == "" {
		t.Fatal("return_to query param is empty")
	}

	// Parse the return_to URL (handoff URL).
	handoff, err := url.Parse(returnTo)
	if err != nil {
		t.Fatalf("return_to is not a valid URL: %v", err)
	}
	if handoff.Path != "/auth/handoff" {
		t.Errorf("handoff path = %q, want %q", handoff.Path, "/auth/handoff")
	}

	redirectURI := handoff.Query().Get("redirect_uri")
	if redirectURI == "" {
		t.Fatal("redirect_uri in handoff URL is empty")
	}

	// Parse the redirect_uri (localhost callback).
	callback, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("redirect_uri is not a valid URL: %v", err)
	}
	if callback.Hostname() != "127.0.0.1" {
		t.Errorf("callback host = %q, want %q", callback.Hostname(), "127.0.0.1")
	}
	if callback.Port() != "12345" {
		t.Errorf("callback port = %q, want %q", callback.Port(), "12345")
	}
	if callback.Path != "/callback" {
		t.Errorf("callback path = %q, want %q", callback.Path, "/callback")
	}
	if callback.Query().Get("state") != "test-state" {
		t.Errorf("callback state = %q, want %q", callback.Query().Get("state"), "test-state")
	}
}

func TestBuildLoginURL_TrailingSlash(t *testing.T) {
	url1 := BuildLoginURL("https://app.dibbla.com/", 8080, "s")
	url2 := BuildLoginURL("https://app.dibbla.com", 8080, "s")
	if url1 != url2 {
		t.Errorf("trailing slash should not affect URL:\n  %s\n  %s", url1, url2)
	}
}

func TestStartCallbackServer_ReceivesToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state := "test-state-123"
	port, resultCh, shutdown := StartCallbackServer(ctx, state)
	defer shutdown()

	if port == 0 {
		t.Fatal("StartCallbackServer returned port 0")
	}

	// Simulate browser callback.
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback?token=test-jwt&state=%s", port, state)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case result := <-resultCh:
		if result.Err != nil {
			t.Fatalf("unexpected error: %v", result.Err)
		}
		if result.Token != "test-jwt" {
			t.Errorf("token = %q, want %q", result.Token, "test-jwt")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for token")
	}
}

func TestStartCallbackServer_RejectsWrongState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	port, _, shutdown := StartCallbackServer(ctx, "correct-state")
	defer shutdown()

	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback?token=jwt&state=wrong-state", port)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestStartCallbackServer_RejectsNonCallbackPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	port, _, shutdown := StartCallbackServer(ctx, "state")
	defer shutdown()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/other", port))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestStartCallbackServer_RejectsNoToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state := "test-state"
	port, resultCh, shutdown := StartCallbackServer(ctx, state)
	defer shutdown()

	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s", port, state)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	select {
	case result := <-resultCh:
		if result.Err == nil {
			t.Error("expected error for missing token")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestExchangeJWTForAPIToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/auth/v1/tokens" {
			t.Errorf("path = %q, want /api/auth/v1/tokens", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-jwt" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-jwt")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"message":"API token created successfully","api_token":{"token":"ak_abc123","id":"at_123","user_id":"u1","expires_at":0}}`)
	}))
	defer server.Close()

	token, err := ExchangeJWTForAPIToken(server.URL, "test-jwt")
	if err != nil {
		t.Fatalf("ExchangeJWTForAPIToken() error: %v", err)
	}
	if token != "ak_abc123" {
		t.Errorf("token = %q, want %q", token, "ak_abc123")
	}
}

func TestExchangeJWTForAPIToken_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"UNAUTHORIZED","message":"Invalid token"}`)
	}))
	defer server.Close()

	_, err := ExchangeJWTForAPIToken(server.URL, "bad-jwt")
	if err == nil {
		t.Fatal("expected error for unauthorized response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain status code: %v", err)
	}
}

func TestDeriveAppURL(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"https://api.dibbla.com", "https://app.dibbla.com", false},
		{"https://api.staging.dibbla.com", "https://app.staging.dibbla.com", false},
		{"https://api.dibbla.com:8443", "https://app.dibbla.com:8443", false},
		{"https://myserver.com", "", true},
		{"https://custom.example.com", "", true},
	}

	for _, tt := range tests {
		got, err := DeriveAppURL(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("DeriveAppURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("DeriveAppURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
