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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/orgctx"
)

// CallbackGracePeriod is how long the local OAuth callback server stays
// alive after writing the success page. Without a grace period, browser
// auto-favicon fetches and reflexive reloads land on a dead listener and
// render ERR_CONNECTION_REFUSED on top of the success message.
//
// Exported so that the caller (browserLogin) can match its own linger
// against this value — without the caller blocking, the listener is
// torn down by defer shutdown() as soon as the orchestrator returns,
// which happens before the timer below has a chance to fire.
const CallbackGracePeriod = 10 * time.Second

// IsSSHSession reports whether the current process is running inside an
// SSH shell. The localhost-callback OAuth flow cannot complete here:
// the callback URL points at this host's loopback, but the user's
// browser is on the other side of the SSH connection. Detection is
// heuristic but reliable — sshd sets SSH_CONNECTION/SSH_TTY for every
// real SSH session.
func IsSSHSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// HasGraphicalSession reports whether xdg-open/open/rundll32 is likely
// to actually launch a browser. On Linux that requires a running
// graphical session ($DISPLAY for X11, $WAYLAND_DISPLAY for Wayland);
// on macOS and Windows a desktop is assumed. When false, callers
// should print the login URL as the primary instruction instead of
// pretending to launch a browser that will silently die.
func HasGraphicalSession() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

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

		// Stay alive briefly so the browser can fetch a favicon or the
		// user can reload the success page without seeing
		// ERR_CONNECTION_REFUSED.
		time.AfterFunc(CallbackGracePeriod, func() {
			srv.Shutdown(context.Background()) //nolint:errcheck
		})
	})

	// Quiet 204 for Chrome's automatic favicon fetch on the success
	// page — otherwise the devtools console logs a confusing error and
	// the connection-refused render races the grace period.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
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

// CLISession is what a browser login now yields: a system-issued session rather
// than a user API token. ID is not secret and is stored in config.yaml so
// logout can end the session; Token is, and goes to the keyring.
type CLISession struct {
	ID    string
	Token string
}

// ExchangeJWTForCLISession uses the short-lived browser JWT to open a CLI
// session. It calls POST /api/auth/v1/cli-sessions with the JWT as a Bearer
// token.
//
// This used to POST /api/auth/v1/tokens and mint a user API token — the same
// kind of credential a person creates by hand in the web UI. Every login left
// another one behind, indistinguishable from the deliberate ones and never
// cleaned up, because logout only forgot the local copy. Sessions are a
// separate table server-side, expire on their own, and can actually be ended.
// See DIB-415.
func ExchangeJWTForCLISession(apiBaseURL, jwt string) (*CLISession, error) {
	apiBaseURL = strings.TrimSuffix(apiBaseURL, "/")
	reqURL := apiBaseURL + "/api/auth/v1/cli-sessions"

	req, err := http.NewRequest("POST", reqURL, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Any organization pinned right now belongs to the session being
	// replaced. Sending it here would fail the membership check and report
	// the login as an organization error.
	req.Header.Set(orgctx.SkipHeader, "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		// The endpoint landed in auth-service before this CLI shipped, so a 404
		// means the server is older than the client. Say so, rather than
		// reporting a bare 404 from a URL the user never typed.
		return nil, fmt.Errorf("this server does not support CLI sessions yet (it predates DIB-415); upgrade it, or log in with --api-key using a token from the web UI")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("session creation failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		CLISession struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"cli_session"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if result.CLISession.Token == "" {
		return nil, fmt.Errorf("server returned an empty CLI session token")
	}

	return &CLISession{ID: result.CLISession.ID, Token: result.CLISession.Token}, nil
}

// RevokeCLISession ends a session server-side. Called by `dibbla logout`, which
// before DIB-416 could only drop its local copy and leave the credential live
// for the rest of its life.
//
// Best-effort by design: the caller forgets the credential locally whether or
// not this succeeds, because a logout that fails to complete must not leave the
// machine still holding a usable token.
func RevokeCLISession(apiBaseURL, token, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("no session id recorded for this context")
	}
	apiBaseURL = strings.TrimSuffix(apiBaseURL, "/")
	reqURL := apiBaseURL + "/api/auth/v1/cli-sessions/" + url.PathEscape(sessionID)

	req, err := http.NewRequest("DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	// The org pin is irrelevant to ending a session, and sending one the
	// session is not a member of would fail the request for the wrong reason.
	req.Header.Set(orgctx.SkipHeader, "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("could not end the session (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
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
