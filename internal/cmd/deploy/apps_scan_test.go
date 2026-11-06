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

func TestScanCLIDependencyReport(t *testing.T) {
	id := strings.Repeat("a", 64)
	full := fmt.Sprintf(`{"scan_id":%q,"status":"completed","coverage":"secrets_and_dependencies","archive_sha256":"%s","gitleaks":"8.30.1","findings":[],
"dependencies":{"status":"partial","packages":2,"syft":"1.52.0","grype":"0.119.0","vuln_db_built_at":"2026-11-06T06:21:59Z",
"files":[{"path":"web/package-lock.json","service":"web","status":"scanned","packages":2},{"path":"worker/bun.lockb","service":"worker","status":"UNSUPPORTED_FORMAT","packages":0}],
"gaps":[{"code":"UNSUPPORTED_FORMAT","path":"worker/bun.lockb","service":"worker","message":"This lock file format is not supported; its dependencies were not checked."}],
"vulnerabilities":[{"id":"GHSA-35jh-r3h4-6jhm","severity":"high","package":"lodash","version":"4.17.20","fixed_in":"4.17.21","file":"web/package-lock.json","service":"web"}],
"not_covered_without_build":["image_and_os_packages"]}}`, id, strings.Repeat("c", 64))
	for _, tc := range []struct {
		name, body string
		want       int
		contains   []string
	}{
		{"full", full, 0, []string{"Coverage: secrets and dependencies", "Dependencies: partial — 2 packages from 2 files (Syft 1.52.0, Grype 0.119.0, database 2026-11-06T06:21Z)",
			"high GHSA-35jh-r3h4-6jhm lodash@4.17.20 (fixed in 4.17.21) web/package-lock.json [web]", "Not checked (1 gaps):", "worker/bun.lockb [worker]: UNSUPPORTED_FORMAT", "Never covered without a build", "(Gitleaks 8.30.1)"}},
		{"missing part", fmt.Sprintf(`{"scan_id":%q,"status":"completed","coverage":"secrets_and_dependencies","findings":[]}`, id), 1, nil},
		{"unknown coverage", fmt.Sprintf(`{"scan_id":%q,"status":"completed","coverage":"everything","findings":[]}`, id), 1, nil},
		{"old server", fmt.Sprintf(`{"scan_id":%q,"status":"completed","coverage":"secrets_only","findings":[]}`, id), 0, []string{"Coverage: secrets only", "Dependencies: not checked by this server"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer s.Close()
			var out, errout bytes.Buffer
			code := runAppsScanCore(&out, &errout, s.URL, "token", "result", "alpha", id, "", "", false)
			if code != tc.want {
				t.Fatalf("exit %d, want %d: %s %s", code, tc.want, &out, &errout)
			}
			for _, c := range tc.contains {
				if !strings.Contains(out.String(), c) {
					t.Errorf("missing %q in:\n%s", c, &out)
				}
			}
		})
	}
}
