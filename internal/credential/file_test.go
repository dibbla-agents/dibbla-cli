package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/cfgdir"
)

// withTempCredFile points the CLI's config directory at a fresh temp dir for
// the duration of the test, so the test never reads or writes the real user's
// credentials file. The seam moved from an unexported tokenFilePath var to
// cfgdir.SetForTest when named contexts arrived, because the config directory
// now holds three kinds of artefact read by three packages, and a test that
// isolates only one of them writes into the developer's real config dir with
// the other two.
func withTempCredFile(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dibbla")
	t.Cleanup(cfgdir.SetForTest(dir))
	return filepath.Join(dir, credFileName)
}

func TestSetTokenFile_RoundTrip(t *testing.T) {
	path := withTempCredFile(t)

	if err := SetTokenFile("ak_test_123", "https://api.example.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	// File should exist and be readable.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected credentials file at %s: %v", path, err)
	}

	token, apiURL, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile: %v", err)
	}
	if token != "ak_test_123" {
		t.Errorf("token = %q, want %q", token, "ak_test_123")
	}
	if apiURL != "https://api.example.com" {
		t.Errorf("apiURL = %q, want %q", apiURL, "https://api.example.com")
	}
}

func TestSetTokenFile_EmptyAPIURL_StoredAsEmpty(t *testing.T) {
	// When the user logs in against the default API, we pass apiURL="".
	// Subsequent reads should return "" (callers treat that as "use
	// default"). Verifies that re-login with default URL doesn't leave
	// a stale custom URL behind.
	path := withTempCredFile(t)

	if err := SetTokenFile("ak_v1", "https://api.staging.example.com"); err != nil {
		t.Fatalf("first SetTokenFile: %v", err)
	}
	if err := SetTokenFile("ak_v2", ""); err != nil {
		t.Fatalf("second SetTokenFile: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "DIBBLA_API_TOKEN=ak_v2") {
		t.Errorf("file should contain new token; got:\n%s", body)
	}
	if strings.Contains(string(body), "api.staging.example.com") {
		t.Errorf("file should not retain stale URL; got:\n%s", body)
	}

	_, apiURL, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile: %v", err)
	}
	if apiURL != "" {
		t.Errorf("apiURL = %q, want empty string", apiURL)
	}
}

func TestSetTokenFile_Mode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits not meaningful on Windows")
	}
	path := withTempCredFile(t)

	if err := SetTokenFile("ak_x", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("mode = %#o, want %#o", perm, 0600)
	}
}

func TestSetTokenFile_ParentDirCreatedAt0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dir mode bits not meaningful on Windows")
	}
	path := withTempCredFile(t)

	if err := SetTokenFile("ak_x", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	st, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	// MkdirAll respects umask; we asked for 0700 which most umasks
	// (0022, 0077) leave at 0700 since they only mask group/other
	// bits. Accept anything ≤ 0700.
	if perm := st.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("parent dir mode = %#o leaks group/other bits", perm)
	}
}

func TestGetTokenFile_NoFile(t *testing.T) {
	withTempCredFile(t) // path points at non-existent file

	token, apiURL, err := GetTokenFile()
	if err != nil {
		t.Errorf("GetTokenFile on missing file should not error, got %v", err)
	}
	if token != "" || apiURL != "" {
		t.Errorf("GetTokenFile on missing file = (%q, %q), want both empty", token, apiURL)
	}
}

func TestDeleteTokenFile_NoFile(t *testing.T) {
	withTempCredFile(t)
	if err := DeleteTokenFile(); err != nil {
		t.Errorf("DeleteTokenFile on missing file should not error, got %v", err)
	}
}

func TestDeleteTokenFile_RemovesIt(t *testing.T) {
	path := withTempCredFile(t)

	if err := SetTokenFile("ak_x", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if err := DeleteTokenFile(); err != nil {
		t.Fatalf("DeleteTokenFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file removed, stat err = %v", err)
	}
}

func TestIsKeyringUnavailable(t *testing.T) {
	// The strings below are the ones godbus v5.1.0 and go-keyring v0.2.6
	// actually produce on a headless Linux host, copied from their source
	// rather than imagined. The previous version of this test asserted
	// "could not connect: dial unix ...", which neither library emits: it was
	// written to match the needle instead of the world, so it passed while
	// the production path it certified was broken on every host it mattered
	// on. If a dependency bump changes these, this test is where it should
	// surface.
	realHeadlessErrors := []struct{ name, msg string }{
		// godbus conn_other.go:18 — no dbus-x11 installed, the common case
		// in a container or a minimal cloud image.
		{"no dbus-launch binary", `exec: "dbus-launch": executable file not found in $PATH`},
		// godbus conn.go:85 / conn_other.go:31
		{"no session bus address", "dbus: couldn't determine address of session bus"},
		// godbus transport_unix.go:54 returns net.Dial's error verbatim.
		{"session bus socket dead", "dial unix /run/user/1000/bus: connect: no such file or directory"},
		// go-keyring/secret_service reaching the bus but finding no provider.
		{"libsecret missing", "The name org.freedesktop.secrets was not provided by any .service files"},
		{"no secret service", "no secret service available"},
		// go-keyring secret_service.go:126, a locked collection that cannot
		// be unlocked because there is nothing to draw a prompt.
		{"locked collection", `failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'`},
	}
	for _, tt := range realHeadlessErrors {
		t.Run(tt.name, func(t *testing.T) {
			got := IsKeyringUnavailable(errors.New(tt.msg))
			// On Linux every one of these must fall back. Elsewhere only the
			// two that name the secret service are recognised, because a
			// keyring failure on macOS or Windows is a real fault rather than
			// a host that never had one.
			want := runtime.GOOS == "linux" ||
				strings.Contains(tt.msg, "org.freedesktop.secrets") ||
				strings.Contains(tt.msg, "no secret service")
			if got != want {
				t.Errorf("IsKeyringUnavailable(%q) = %v, want %v on %s", tt.msg, got, want, runtime.GOOS)
			}
		})
	}

	t.Run("nil is not a failure", func(t *testing.T) {
		if IsKeyringUnavailable(nil) {
			t.Error("nil must not read as an unavailable keyring")
		}
	})

	t.Run("the sentinel is recognised without text matching", func(t *testing.T) {
		wrapped := fmt.Errorf("the OS keyring did not respond within 2s: %w", ErrKeyringUnavailable)
		if !IsKeyringUnavailable(wrapped) {
			t.Error("a wrapped ErrKeyringUnavailable must be recognised")
		}
	})

	// A keyring that exists and was refused is NOT an absent keyring. Writing
	// a plaintext copy of the token here would override a decision the user
	// had just made by hand, so these keep failing on every platform.
	t.Run("a refused prompt is kept as a failure", func(t *testing.T) {
		for _, msg := range []string{
			"user dismissed the unlock prompt",
			"prompt was cancelled by the user",
			"access denied",
			"operation not permitted",
		} {
			if IsKeyringUnavailable(errors.New(msg)) {
				t.Errorf("%q must not trigger a silent plaintext fallback", msg)
			}
		}
	})
}

// Regression: GetTokenFile must tolerate a hand-edited file with
// quotes, comments, and whitespace — godotenv handles all of these,
// but this test pins that contract so we don't accidentally swap
// parsers later.
func TestGetTokenFile_ToleratesCommentsAndQuotes(t *testing.T) {
	path := withTempCredFile(t)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("# Dibbla credentials\n  %s = \"ak_quoted\"\n%s='https://api.example.com'\n",
		fileTokenKey, fileAPIURLKey)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	token, apiURL, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile: %v", err)
	}
	if token != "ak_quoted" {
		t.Errorf("token = %q, want %q", token, "ak_quoted")
	}
	if apiURL != "https://api.example.com" {
		t.Errorf("apiURL = %q, want %q", apiURL, "https://api.example.com")
	}
}
