package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The accumulation this whole change exists to stop (DIB-416): `dibbla login`
// used to open a new server-side session on every run, including when the one
// it already held still worked, and nothing ever closed the old ones.
//
// These test reusableContextCredential directly rather than runLogin, which
// calls os.Exit and drives a browser. The reuse decision is the part that
// determines whether a second session gets created at all.

// validateServer stands in for auth-service's token validate endpoint.
func validateServer(t *testing.T, accept bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accept {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"INVALID_TOKEN","message":"Invalid or expired"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user_id":"u1","email":"a@b.c","organization_id":"o1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginReusesACredentialThatStillWorks(t *testing.T) {
	statusIsolate(t)
	srv := validateServer(t, true)

	if _, _, _, err := storeLoginAsContext(srv.URL, "ds_still_good", "cs_1"); err != nil {
		t.Fatalf("seed login: %v", err)
	}

	name, ok := reusableContextCredential(srv.URL)
	if !ok {
		t.Fatal("a stored credential that still validates was not reused — login would open a second session")
	}
	if name == "" {
		t.Error("reuse reported no context name")
	}
}

// The other half: when the stored credential is dead, login must go ahead and
// get a new one rather than reusing something that will fail on first use.
func TestLoginDoesNotReuseARejectedCredential(t *testing.T) {
	statusIsolate(t)
	srv := validateServer(t, false)

	if _, _, _, err := storeLoginAsContext(srv.URL, "ds_expired", "cs_2"); err != nil {
		t.Fatalf("seed login: %v", err)
	}

	if _, ok := reusableContextCredential(srv.URL); ok {
		t.Error("a credential the server rejects was reused — the next command would fail instead of the login")
	}
}

func TestLoginHasNothingToReuseOnAFreshMachine(t *testing.T) {
	statusIsolate(t)
	srv := validateServer(t, true)

	if _, ok := reusableContextCredential(srv.URL); ok {
		t.Error("reported a reusable credential with no context stored")
	}
}

// A context created by --api-key holds a user API token and records no session
// id. It is still perfectly reusable — the reuse decision is about whether the
// credential works, not about how it was obtained.
func TestLoginReusesAnAPIKeyContextToo(t *testing.T) {
	statusIsolate(t)
	srv := validateServer(t, true)

	if _, _, _, err := storeLoginAsContext(srv.URL, "ak_pasted_by_hand", ""); err != nil {
		t.Fatalf("seed login: %v", err)
	}

	if _, ok := reusableContextCredential(srv.URL); !ok {
		t.Error("an --api-key context with a working token was not reused")
	}
}

// Reuse means the command does nothing, which is only correct when nothing was
// asked for. Each flag below requests work that a short-circuit would silently
// skip — and it would skip it while printing success.
func TestFlagsThatRequestWorkDisableReuse(t *testing.T) {
	reset := func() {
		loginNewToken, loginContext, loginWriteEnv, loginNoKeychain = false, "", false, false
	}
	t.Cleanup(reset)

	reset()
	if !canReuseExistingLogin() {
		t.Error("a bare `dibbla login` should be allowed to reuse")
	}

	for name, set := range map[string]func(){
		"--new-token":   func() { loginNewToken = true },
		"--context":     func() { loginContext = "other" },
		"--write-env":   func() { loginWriteEnv = true },
		"--no-keychain": func() { loginNoKeychain = true },
	} {
		reset()
		set()
		if canReuseExistingLogin() {
			t.Errorf("%s must disable reuse: it asks for work that reuse would skip", name)
		}
	}
	reset()
}
