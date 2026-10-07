package storage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// IssueBucketCredentials asks the route DIB-1344 added, as the caller, and
// reads the key it answers with.
func TestIssueBucketCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/deploy/buckets/my-uploads/credentials" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"endpoint":"https://s3.example.com","bucket":"my-uploads","access_key_id":"AK","secret_access_key":"SK","session_token":"ST","expires_at":"2026-10-07T13:00:00Z"}`))
	}))
	defer srv.Close()

	c, err := IssueBucketCredentials(srv.URL, "tok", "my-uploads")
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "https://s3.example.com" || c.Bucket != "my-uploads" || c.AccessKeyID != "AK" || c.SecretAccessKey != "SK" || c.SessionToken != "ST" ||
		!c.ExpiresAt.Equal(time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("credentials = %+v", c)
	}
}

func TestIssueBucketCredentials_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{"bucket not found", http.StatusNotFound, `{"status":"error","error":{"code":"BUCKET_NOT_FOUND","message":"Bucket not found: my-uploads"}}`, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "BUCKET_NOT_FOUND") || errors.Is(err, ErrBucketKeysUnsupported) {
				t.Errorf("err = %v", err)
			}
		}},
		{"server without the route", http.StatusNotFound, `404 page not found`, func(t *testing.T, err error) {
			if !errors.Is(err, ErrBucketKeysUnsupported) {
				t.Errorf("err = %v, want ErrBucketKeysUnsupported", err)
			}
		}},
		{"viewer", http.StatusForbidden, `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"your role (viewer) cannot do this"}}`, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "ROLE_FORBIDDEN") {
				t.Errorf("err = %v", err)
			}
		}},
		{"answer without a key", http.StatusOK, `{"bucket":"my-uploads"}`, func(t *testing.T, err error) {
			if err == nil {
				t.Error("a keyless answer was accepted")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			_, err := IssueBucketCredentials(srv.URL, "tok", "my-uploads")
			c.check(t, err)
		})
	}
}
