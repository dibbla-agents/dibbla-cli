package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withTempCredFile redirects tokenFilePath to a fresh temp dir for the
// duration of the test, so the test never reads or writes the real
// user's credentials file.
func withTempCredFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dibbla", credFileName)

	orig := tokenFilePath
	tokenFilePath = func() string { return path }
	t.Cleanup(func() { tokenFilePath = orig })

	return path
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
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// The exact wording from go-keyring on a Linux box without
		// libsecret/gnome-keyring — the failure that motivated this
		// whole change.
		{"libsecret missing", errors.New("The name org.freedesktop.secrets was not provided by any .service files"), true},
		{"no secret service", errors.New("no secret service available"), true},
		{"dbus socket missing", errors.New("could not connect: dial unix /run/dbus: connect: no such file or directory"), true},
		{"unrelated error", errors.New("user dismissed the unlock prompt"), false},
		{"item not found", errors.New("Item not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKeyringUnavailable(tt.err); got != tt.want {
				t.Errorf("IsKeyringUnavailable(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
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
