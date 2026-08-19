package mcp

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// stubMCP fakes the community endpoint: happy path answers initialize and
// community_whoami over SSE framing (like the real stateless server); status
// != 0 makes every request answer that HTTP status instead.
func stubMCP(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"code":"X"}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer ak_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body.String(), `"initialize"`) {
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":\"dibbla-community\",\"version\":\"1.0.0\"}}}\n")
			return
		}
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"isError\":false,\"content\":[{\"type\":\"text\",\"text\":\"Acting as **erik** on community.dibbla.com\"}]}}\n")
	}))
}

func TestCommunityCheck(t *testing.T) {
	t.Run("happy path prints server and identity", func(t *testing.T) {
		srv := stubMCP(0)
		defer srv.Close()
		t.Setenv("DIBBLA_MCP_URL", srv.URL)
		t.Setenv("DIBBLA_API_TOKEN", "ak_test")
		var buf bytes.Buffer
		if err := runCommunityCheck(&buf); err != nil {
			t.Fatalf("check failed: %v\n%s", err, buf.String())
		}
		out := buf.String()
		for _, want := range []string{"dibbla-community 1.0.0", "Acting as **erik**", "from the DIBBLA_API_TOKEN environment variable"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("401 names the token as the problem", func(t *testing.T) {
		srv := stubMCP(http.StatusUnauthorized)
		defer srv.Close()
		t.Setenv("DIBBLA_MCP_URL", srv.URL)
		t.Setenv("DIBBLA_API_TOKEN", "ak_wrong")
		err := runCommunityCheck(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "rejected the token") {
			t.Errorf("want token-rejection error, got: %v", err)
		}
	})

	t.Run("404 explains the install gate", func(t *testing.T) {
		srv := stubMCP(http.StatusNotFound)
		defer srv.Close()
		t.Setenv("DIBBLA_MCP_URL", srv.URL)
		t.Setenv("DIBBLA_API_TOKEN", "ak_test")
		err := runCommunityCheck(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "central-install") {
			t.Errorf("want install-gate explanation, got: %v", err)
		}
	})

	t.Run("missing token is actionable", func(t *testing.T) {
		// Point at a closed port so the test can never reach a live endpoint:
		// on a developer box config.Load may find a real keyring token, and
		// this test must stay hermetic either way.
		t.Setenv("DIBBLA_MCP_URL", "http://127.0.0.1:1")
		t.Setenv("DIBBLA_API_TOKEN", "")
		err := runCommunityCheck(&bytes.Buffer{})
		if err == nil {
			t.Fatal("check against a dead endpoint cannot succeed")
		}
		msg := err.Error()
		if !strings.Contains(msg, "dibbla login") && !strings.Contains(msg, "cannot reach") {
			t.Errorf("want a login hint (no token) or an unreachable-endpoint error (token found locally), got: %v", err)
		}
	})
}

func TestCheckFailsWithoutExportedEnvVar(t *testing.T) {
	// The A1 scenario from the 2026-08-19 review: the token resolves via a
	// local store, the server chain verifies, but DIBBLA_API_TOKEN is not in
	// the environment — the state where every emitted client config yields
	// 401. The check must fail loudly, naming the env var.
	if runtime.GOOS != "linux" || platform.IsCI() {
		// os.UserConfigDir honors XDG_CONFIG_HOME only on Linux, and
		// config.Load skips local stores entirely on CI.
		t.Skip("needs Linux outside CI (XDG_CONFIG_HOME-based credentials file)")
	}
	srv := stubMCP(0)
	defer srv.Close()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "dibbla"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "dibbla", "credentials.env"),
		[]byte("DIBBLA_API_TOKEN=ak_test\nDIBBLA_API_URL=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("DIBBLA_MCP_URL", srv.URL)
	t.Setenv("DIBBLA_API_TOKEN", "")

	var buf bytes.Buffer
	err := runCommunityCheck(&buf)
	if err == nil {
		t.Fatalf("check must fail when the env var is not exported; output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "${DIBBLA_API_TOKEN}") {
		t.Errorf("the error must name the env var, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "from the credentials file") {
		t.Errorf("the token source must be named, got:\n%s", out)
	}
	if !strings.Contains(out, "NOT exported") {
		t.Errorf("the warning must be visible in the output, got:\n%s", out)
	}
	// The chain itself still verified — identity must have printed.
	if !strings.Contains(out, "Acting as **erik**") {
		t.Errorf("the server chain should verify before the env failure, got:\n%s", out)
	}
}

func TestClaudeOneLinerQuoting(t *testing.T) {
	out := render(t, "claude")
	if !strings.Contains(out, `'Authorization: Bearer ${DIBBLA_API_TOKEN}'`) {
		t.Errorf("the one-liner must single-quote the header so the shell cannot expand the placeholder:\n%s", out)
	}
	if strings.Contains(out, `--header "Authorization`) {
		t.Errorf("double-quoted --header found — shell would expand the token prematurely:\n%s", out)
	}
}
