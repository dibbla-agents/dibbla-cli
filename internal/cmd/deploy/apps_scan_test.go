package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanCLIStartReplayAndJSON(t *testing.T) {
	id := strings.Repeat("a", 64)
	key := "1793970000-" + strings.Repeat("b", 32)
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	os.WriteFile(archive, []byte("archive-content"), 0600)
	puts := 0
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		if r.Method == "POST" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["request_id"] != key || len(body["archive_sha256"]) != 64 {
				t.Errorf("wrong start: %v", body)
			}
			state := "created"
			if completed {
				state = "completed"
			}
			fmt.Fprintf(w, `{"scan_id":%q,"status":%q,"coverage":"secrets_only"}`, id, state)
		} else if r.Method == "PUT" {
			puts++
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != "archive-content" || r.Header.Get("Content-Type") != "application/gzip" {
				t.Error("upload corrupted")
			}
			completed = true
			fmt.Fprintf(w, `{"scan_id":%q,"status":"completed","coverage":"secrets_only"}`, id)
		} else {
			t.Errorf("unexpected request: %v", r.URL)
		}
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		var out, errout bytes.Buffer
		code := runAppsScanCore(&out, &errout, server.URL, "token", "start", "alpha", "", archive, key, true)
		if code != 0 || !json.Valid(out.Bytes()) || !strings.Contains(errout.String(), key) {
			t.Fatalf("%d %s %s", code, &out, &errout)
		}
	}
	if puts != 1 {
		t.Fatalf("reuploaded completed run: %d", puts)
	}
}
func TestScanCLIFailureGoneAndCompleteText(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, tc := range []struct {
		status   int
		body     string
		action   string
		want     int
		contains string
	}{
		{200, fmt.Sprintf(`{"scan_id":%q,"status":"failed","error_code":"SCANNER_FAILED","coverage":"secrets_only"}`, id), "status", 1, "SCANNER_FAILED"},
		{410, `{"error":{"code":"SCAN_GONE","message":"expired"}}`, "result", 1, "SCAN_GONE"},
		{200, fmt.Sprintf(`{"scan_id":%q,"status":"completed","coverage":"secrets_only","findings":[{"file":"config.env","line":7,"rule":"test-rule"}]}`, id), "result", 0, "config.env:7 test-rule"},
	} {
		t.Run(tc.action+fmt.Sprint(tc.status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			for _, jsonOut := range []bool{false, true} {
				var out, errout bytes.Buffer
				code := runAppsScanCore(&out, &errout, s.URL, "token", tc.action, "alpha", id, "", "", jsonOut)
				if code != tc.want {
					t.Fatalf("exit %d: %s %s", code, &out, &errout)
				}
				if jsonOut && !json.Valid(out.Bytes()) {
					t.Fatalf("invalid JSON: %s", &out)
				}
				if !jsonOut && !strings.Contains(out.String()+errout.String(), tc.contains) {
					t.Fatalf("lost result: %s %s", &out, &errout)
				}
			}
		})
	}
}
