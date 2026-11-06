package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

func TestFindingsCLIAllPagesAndAtomicFailure(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/deploy/deployments/alpha/security-scan/findings" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("wrong request: %s", r.URL)
				}
				q := r.URL.Query()
				if q.Get("limit") != "1" || q.Get("min_severity") != "high" || q.Get("fixable_only") != "true" {
					t.Errorf("lost query: %v", q)
				}
				if q.Get("cursor") == "page2" && changed {
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":{"code":"SCAN_CHANGED","message":"Discard pages and restart"}}`)
					return
				}
				v := apps.SecurityFindingsView{Alias: "alpha", ScanID: "scan-1", DeploymentID: "dep_1", Status: "partial", Errors: []string{"worker scan failed"}, Total: 2, Limit: 1, Findings: []apps.AgentFinding{{Kind: "vulnerability", ID: "CVE-123", Package: "lib", Version: "1", Service: "web", Severity: "high", FixedIn: "2", Advisory: "https://advisory.example"}}}
				if q.Get("cursor") == "" {
					v.HasMore = true
					v.NextCursor = "page2"
				} else {
					v.Offset = 1
					v.Findings = []apps.AgentFinding{{Kind: "secret", ID: "test-rule", File: "config.env", Line: 7}}
				}
				json.NewEncoder(w).Encode(v)
			}))
			defer s.Close()
			var stdout, stderr bytes.Buffer
			code := runAppsFindingsCore(&stdout, &stderr, s.URL, "token", "alpha", apps.FindingsOptions{Limit: 1, MinSeverity: "high", FixableOnly: true}, true, true)
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
			if changed {
				if code != 6 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "SCAN_CHANGED") {
					t.Fatalf("partial output: code=%d out=%s err=%s", code, &stdout, &stderr)
				}
				return
			}
			var v apps.SecurityFindingsView
			if code != 0 || json.Unmarshal(stdout.Bytes(), &v) != nil || len(v.Findings) != 2 || v.HasMore || v.Status != "partial" || v.Findings[0].Advisory != "https://advisory.example" || v.Findings[1].Line != 7 {
				t.Fatalf("lost facts: %d %s %s", code, &stdout, &stderr)
			}
		})
	}
}

func TestFindingsCLIStatusAndContinuationInText(t *testing.T) {
	for _, status := range []string{"missing", "running", "partial", "failed", "unavailable"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"status":%q,"deployment_id":"dep_1","running":false,"errors":["scan diagnostic"],"findings":[],"total":0,"has_more":false}`, status)
		}))
		var out, errout bytes.Buffer
		code := runAppsFindingsCore(&out, &errout, s.URL, "token", "alpha", apps.FindingsOptions{}, false, false)
		s.Close()
		if code != 0 || !strings.Contains(out.String(), status) || !strings.Contains(out.String(), "not a complete result") || !strings.Contains(out.String(), "scan diagnostic") {
			t.Fatalf("%s: %d %s %s", status, code, &out, &errout)
		}
	}
}

func TestFindingsCLIExplicitFalseFilterIsNotLost(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("fixable_only") || r.URL.Query().Get("fixable_only") != "false" {
			t.Error("explicit false was lost")
		}
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"cursor filters must stay unchanged"}}`)
	}))
	defer s.Close()
	var out, errout bytes.Buffer
	code := runAppsFindingsCore(&out, &errout, s.URL, "token", "alpha", apps.FindingsOptions{Cursor: "filtered-page", FixableOnlySet: true}, true, false)
	if code != 5 || out.Len() != 0 {
		t.Fatalf("wrong refusal: %d %s %s", code, &out, &errout)
	}
}
