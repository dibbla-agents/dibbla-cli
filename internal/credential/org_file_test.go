package credential

import (
	"os"
	"strings"
	"testing"
)

func TestSetOrgFile_RoundTrip(t *testing.T) {
	withTempCredFile(t)

	if err := SetOrgFile("id-acme", "Acme"); err != nil {
		t.Fatalf("SetOrgFile: %v", err)
	}
	id, name, err := GetOrgFile()
	if err != nil {
		t.Fatalf("GetOrgFile: %v", err)
	}
	if id != "id-acme" || name != "Acme" {
		t.Errorf("got (%q, %q), want (id-acme, Acme)", id, name)
	}
}

// Pinning an org is not a login. Writing the org must leave the token alone,
// or switching org would log the user out.
func TestSetOrgFile_PreservesToken(t *testing.T) {
	withTempCredFile(t)

	if err := SetTokenFile("ak_test_123", "https://api.example.com"); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}
	if err := SetOrgFile("id-acme", "Acme"); err != nil {
		t.Fatalf("SetOrgFile: %v", err)
	}

	token, apiURL, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile: %v", err)
	}
	if token != "ak_test_123" {
		t.Errorf("token = %q, want ak_test_123", token)
	}
	if apiURL != "https://api.example.com" {
		t.Errorf("apiURL = %q, want https://api.example.com", apiURL)
	}
}

// And the reverse: a later login must not resurrect the previous org.
func TestSetTokenFile_LeavesOrgKeysAlone(t *testing.T) {
	withTempCredFile(t)

	if err := SetOrgFile("id-acme", "Acme"); err != nil {
		t.Fatalf("SetOrgFile: %v", err)
	}
	if err := SetTokenFile("ak_new", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}

	id, _, err := GetOrgFile()
	if err != nil {
		t.Fatalf("GetOrgFile: %v", err)
	}
	if id != "id-acme" {
		t.Errorf("org id = %q, want id-acme — SetTokenFile should only touch token/URL", id)
	}
}

func TestDeleteOrgFile_ClearsPinButKeepsToken(t *testing.T) {
	withTempCredFile(t)

	if err := SetTokenFile("ak_test_123", ""); err != nil {
		t.Fatalf("SetTokenFile: %v", err)
	}
	if err := SetOrgFile("id-acme", "Acme"); err != nil {
		t.Fatalf("SetOrgFile: %v", err)
	}
	if err := DeleteOrgFile(); err != nil {
		t.Fatalf("DeleteOrgFile: %v", err)
	}

	id, name, err := GetOrgFile()
	if err != nil {
		t.Fatalf("GetOrgFile: %v", err)
	}
	if id != "" || name != "" {
		t.Errorf("got (%q, %q), want both empty", id, name)
	}
	token, _, err := GetTokenFile()
	if err != nil {
		t.Fatalf("GetTokenFile: %v", err)
	}
	if token != "ak_test_123" {
		t.Errorf("token = %q, want it untouched by clearing the org", token)
	}
}

// `dibbla org clear` on a host that never wrote the file is success, not an
// error about a missing file.
func TestDeleteOrgFile_NoFileIsNotAnError(t *testing.T) {
	path := withTempCredFile(t)

	if err := DeleteOrgFile(); err != nil {
		t.Fatalf("DeleteOrgFile with no file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("DeleteOrgFile created the credentials file; it should not")
	}
}

func TestGetOrgFile_NoFile(t *testing.T) {
	withTempCredFile(t)

	id, name, err := GetOrgFile()
	if err != nil {
		t.Fatalf("GetOrgFile: %v", err)
	}
	if id != "" || name != "" {
		t.Errorf("got (%q, %q), want both empty", id, name)
	}
}

func TestSetOrgFile_WritesExpectedKeys(t *testing.T) {
	path := withTempCredFile(t)

	if err := SetOrgFile("id-acme", "Acme"); err != nil {
		t.Fatalf("SetOrgFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "DIBBLA_ORG_ID=id-acme") {
		t.Errorf("org id line missing, got:\n%s", got)
	}
	if !strings.Contains(got, "DIBBLA_ORG_NAME=Acme") {
		t.Errorf("org name line missing, got:\n%s", got)
	}
}
