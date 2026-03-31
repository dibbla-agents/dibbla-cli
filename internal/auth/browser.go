package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// CallbackResult holds the token received from the browser callback.
type CallbackResult struct {
	Token string
	Err   error
}

// GenerateState creates a cryptographically random state nonce for CSRF protection.
func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

// StartCallbackServer starts a temporary HTTP server on a random localhost port.
// It serves a single /callback endpoint that validates the state parameter and
// reads the token. The server shuts down after one successful callback or when
// the context is cancelled.
func StartCallbackServer(ctx context.Context, expectedState string) (int, <-chan CallbackResult, func()) {
	resultCh := make(chan CallbackResult, 1)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		resultCh <- CallbackResult{Err: fmt.Errorf("failed to start callback server: %w", err)}
		close(resultCh)
		return 0, resultCh, func() {}
	}

	port := listener.Addr().(*net.TCPAddr).Port

	var once sync.Once
	mux := http.NewServeMux()

	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state := r.URL.Query().Get("state")
		if state != expectedState {
			http.Error(w, "invalid state parameter", http.StatusForbidden)
			return
		}

		token := r.URL.Query().Get("token")
		if token == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, callbackErrorHTML)
			once.Do(func() {
				resultCh <- CallbackResult{Err: fmt.Errorf("no token received from browser")}
				close(resultCh)
			})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, callbackSuccessHTML)

		once.Do(func() {
			resultCh <- CallbackResult{Token: token}
			close(resultCh)
		})

		// Shut down after responding.
		go srv.Shutdown(context.Background()) //nolint:errcheck
	})

	// 404 for all other paths.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			once.Do(func() {
				resultCh <- CallbackResult{Err: fmt.Errorf("callback server error: %w", err)}
				close(resultCh)
			})
		}
	}()

	// Shut down on context cancellation.
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background()) //nolint:errcheck
		once.Do(func() {
			resultCh <- CallbackResult{Err: ctx.Err()}
			close(resultCh)
		})
	}()

	shutdown := func() {
		srv.Shutdown(context.Background()) //nolint:errcheck
	}

	return port, resultCh, shutdown
}

// BuildLoginURL constructs the browser login URL that chains through OAuth → handoff → localhost callback.
// Uses absolute URLs for return_to so that buildOnboardingRedirect in auth-service preserves it.
func BuildLoginURL(appBaseURL string, port int, state string) string {
	appBaseURL = strings.TrimSuffix(appBaseURL, "/")

	// The localhost callback URL where the handoff endpoint will redirect with the JWT.
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s", port, url.QueryEscape(state))

	// The handoff endpoint URL that reads the auth cookie and redirects to the callback.
	handoffURL := appBaseURL + "/auth/handoff?redirect_uri=" + url.QueryEscape(callbackURL)

	// The login page URL with return_to pointing to the handoff.
	loginURL := appBaseURL + "/login?return_to=" + url.QueryEscape(handoffURL)

	return loginURL
}

// OpenBrowser attempts to open the given URL in the default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// CopyToClipboard copies text to the system clipboard.
func CopyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case "windows":
		cmd = exec.Command("clip")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// ExchangeJWTForAPIToken uses a short-lived JWT to create a long-lived API token.
// It calls POST /api/auth/v1/tokens with the JWT as a Bearer token.
func ExchangeJWTForAPIToken(apiBaseURL, jwt string) (string, error) {
	apiBaseURL = strings.TrimSuffix(apiBaseURL, "/")
	reqURL := apiBaseURL + "/api/auth/v1/tokens"

	req, err := http.NewRequest("POST", reqURL, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token creation failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		APIToken struct {
			Token string `json:"token"`
		} `json:"api_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if result.APIToken.Token == "" {
		return "", fmt.Errorf("server returned empty API token")
	}

	return result.APIToken.Token, nil
}

// DeriveAppURL attempts to derive the app URL from an API URL by replacing "api." with "app.".
// Returns an error if the URL doesn't follow the expected pattern.
func DeriveAppURL(apiURL string) (string, error) {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("invalid API URL: %w", err)
	}
	host := parsed.Hostname()
	port := parsed.Port()

	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("cannot derive app URL from %q: expected hostname starting with 'api.'", apiURL)
	}

	appHost := "app." + strings.TrimPrefix(host, "api.")
	if port != "" {
		appHost += ":" + port
	}

	return parsed.Scheme + "://" + appHost, nil
}

// Minimal HTML responses for the callback — no external resources to prevent referer leakage.
const callbackSuccessHTML = `<!DOCTYPE html>
<html><head><title>Login Successful</title></head>
<body style="font-family:system-ui,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#111;color:#fff">
<div style="text-align:center"><h2>Login successful!</h2><p>You can close this tab and return to your terminal.</p></div>
</body></html>`

const callbackErrorHTML = `<!DOCTYPE html>
<html><head><title>Login Failed</title></head>
<body style="font-family:system-ui,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#111;color:#fff">
<div style="text-align:center"><h2>Login failed</h2><p>No token was received. Please try again.</p></div>
</body></html>`
