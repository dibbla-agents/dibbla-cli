package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const compareBody = `{"alias":"alpha","base":{"scan_id":"%[1]s","vuln_db_built_at":"2026-11-06T06:21:00Z"},"head":{"scan_id":"%[2]s","vuln_db_built_at":"2026-11-06T06:21:00Z"},
"comparable":{"database":"same","dependency_coverage":"changed","secret_scanner":"same"},
"limitations":[{"code":"COVERAGE_CHANGED","path":"api/go.mod","service":"api","message":"Coverage of this file differs."}],
"summary":{"new":0,"persisting":1,"not_detected":1,"unverifiable":1,"newly_covered":0},
"findings":[
 {"kind":"dependency","change":"persisting","id":"GHSA-p","severity":"High","package":"lodash","service":"api","base_dependency":{"versions":["4.17.19"]},"head_dependency":{"versions":["4.17.20"]},"fixed_in":"4.17.21"},
 {"kind":"dependency","change":"unverifiable","reason":"FILE_NOT_IN_HEAD","id":"GO-1","severity":"High","package":"golang.org/x/net","service":"api","base_dependency":{"versions":["v0.17.0"]}},
 {"kind":"dependency","change":"not_detected","attribution":"submitted_code","id":"GHSA-p","severity":"High","package":"lodash","service":"web","base_dependency":{"versions":["4.17.20"]}}],
"image":{"status":"rematched","message":"These findings are from the running revision's images. Absence from a source report never resolves them.","origin":{"deployment_id":"dep_1","commit_sha":"abc1234"},"vuln_db_built_at":"2026-11-06T12:00:00Z",
 "images":[{"service":"web","image":"registry/alpha:dep_1","vulnerabilities":[{"id":"CVE-1","severity":"Critical","package":"openssl","version":"3.0","type":"apk","in_head_source":"os_package"}]}]},
"retention":"Computed for this request and not stored."}`

func TestScanCLICompareTextJSONAndGone(t *testing.T) {
	base, head := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var gotQuery string
	status, body := 200, fmt.Sprintf(compareBody, base, head)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/deployments/alpha/security-scan/tests/compare") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	var out, errout bytes.Buffer
	if code := runAppsScanCompare(&out, &errout, server.URL, "token", "alpha", base, head, true, false); code != 0 {
		t.Fatalf("%d %s", code, &errout)
	}
	if !strings.Contains(gotQuery, "image=rematch") || !strings.Contains(gotQuery, "base="+base) {
		t.Fatalf("query %s", gotQuery)
	}
	text := out.String()
	for _, want := range []string{
		"0 new, 1 persisting, 1 not detected, 1 unverifiable, 0 newly covered",
		"High GHSA-p lodash [api] base 4.17.19 head 4.17.20 (fixed in 4.17.21)",
		"Unverifiable (head could not check them; absence proves nothing):",
		"GO-1 golang.org/x/net [api] base v0.17.0 — FILE_NOT_IN_HEAD",
		"Not detected in head:\n  High GHSA-p lodash [web] base 4.17.20 — submitted code",
		"COVERAGE_CHANGED api/go.mod [api]",
		"Image background: rematched\n  From running revision dep_1 (commit abc1234), database 2026-11-06T12:00Z",
		"Critical CVE-1 openssl@3.0 (no fix) — OS package",
		"never resolves",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	// not_detected is printed last.
	if strings.Index(text, "Not detected") < strings.Index(text, "Unverifiable") {
		t.Fatal("order")
	}

	out.Reset()
	if code := runAppsScanCompare(&out, &errout, server.URL, "token", "alpha", base, head, false, true); code != 0 || !json.Valid(out.Bytes()) || strings.Contains(gotQuery, "image") {
		t.Fatalf("json: %d %s %s", code, &out, gotQuery)
	}

	// A response lacking parts is refused, not shown as an empty comparison.
	body = fmt.Sprintf(`{"alias":"alpha","base":{"scan_id":%q},"head":{"scan_id":%q},"findings":[]}`, base, head)
	out.Reset()
	errout.Reset()
	if code := runAppsScanCompare(&out, &errout, server.URL, "token", "alpha", base, head, false, false); code == 0 || !strings.Contains(errout.String(), "incomplete") {
		t.Fatalf("incomplete accepted: %d %s %s", code, &out, &errout)
	}

	// An ended report names its side and exits non-zero; --json prints the error.
	status, body = 410, `{"error":{"code":"SCAN_GONE","message":"The head report: Security scan is expired, closed or unavailable; start a new run"}}`
	for _, jsonOut := range []bool{false, true} {
		out.Reset()
		errout.Reset()
		code := runAppsScanCompare(&out, &errout, server.URL, "token", "alpha", base, head, false, jsonOut)
		if code == 0 || !strings.Contains(errout.String()+out.String(), "head report") {
			t.Fatalf("gone: %d %s %s", code, &out, &errout)
		}
		if jsonOut && !json.Valid(out.Bytes()) {
			t.Fatalf("json error: %s", &out)
		}
	}

	if code := runAppsScanCompare(&out, &errout, server.URL, "token", "alpha", base, base, false, false); code != 5 {
		t.Fatalf("same IDs: %d", code)
	}
}
