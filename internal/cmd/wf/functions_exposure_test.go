package wf

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// The exposure commands (DIB-764) are thin over the slim API, so what is
// worth pinning is the wire: the request each one sends, and what the caller
// sees when the server refuses. The stub records every request and answers
// from a fixed script per route.

type exposureStub struct {
	mu       sync.Mutex
	requests []string // "METHOD path?query"
	bodies   []map[string]interface{}
	// putStatus/putBody override the PUT answer to script a refusal.
	putStatus int
	putBody   string
}

func (s *exposureStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/wf/slim/tool-exposures":
			_, _ = w.Write([]byte(`{"exposures":[
				{"server":"http","function_name":"get","subject_kind":"function","min_role":"viewer","enabled":true,"registered":true},
				{"server":"org-worker","function_name":"lookup_customer","subject_kind":"function","min_role":"developer","enabled":false,"registered":false}
			]}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/wf/slim/tool-exposures/"):
			raw, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(raw, &body)
			s.bodies = append(s.bodies, body)
			if s.putStatus != 0 {
				w.WriteHeader(s.putStatus)
				_, _ = w.Write([]byte(s.putBody))
				return
			}
			_, _ = w.Write([]byte(`{"exposure":{"server":"http","function_name":"get","subject_kind":"function","min_role":"admin","enabled":false,"registered":true}}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/wf/slim/tool-exposures/"):
			_, _ = w.Write([]byte(`{"status":"deleted","server":"http","function_name":"get"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/wf/slim/tool-invocations":
			_, _ = w.Write([]byte(`{"invocations":[
				{"id":"inv-1","source":"mcp","server":"http","function_name":"get","triggered_by_user_id":"user-7","status":"completed","started_at":"2026-09-07T10:00:00Z","duration_ms":123,"error_code":null,"failure_summary":null},
				{"id":"inv-2","source":"api","server":"http","function_name":"get","triggered_by_user_id":"user-7","status":"error","started_at":"2026-09-07T09:59:00Z","duration_ms":1500,"error_code":"UPSTREAM_FAILED","failure_summary":"upstream said no"}
			],"count":2,"limit":50,"offset":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/wf/slim/tool-invocations/inv-1":
			_, _ = w.Write([]byte(`{"id":"inv-1","source":"mcp","server":"http","function_name":"get","status":"completed","started_at":"2026-09-07T10:00:00Z","duration_ms":123}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/wf/slim/tool-invocations/inv-1/logs":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"ts":"2026-09-07T10:00:00Z","line":"INFO calling http get","labels":{"run":"inv-1"}}` + "\n"))
			_, _ = w.Write([]byte(`{"ts":"2026-09-07T10:00:01Z","line":"INFO run completed","labels":{"event":"run_completed"}}` + "\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found","code":"NOT_FOUND"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// columnGap matches the padding tabwriter puts between table columns, so a
// two-word header like "MIN ROLE" survives the normalisation.
var columnGap = regexp.MustCompile(`\s{2,}`)

// useStub points both the apiclient and the logs streamer (which reads the
// API URL from the environment) at the stub, and resets the output flag.
func useStub(t *testing.T, stub *exposureStub) {
	t.Helper()
	srv := stub.server(t)
	previous := apiClient
	apiClient = apiclient.NewClient(srv.URL, "test-token", false)
	t.Setenv("DIBBLA_API_TOKEN", "test-token")
	t.Setenv("DIBBLA_API_URL", srv.URL)
	previousOutput := flagOutput
	flagOutput = ""
	t.Cleanup(func() {
		apiClient = previous
		flagOutput = previousOutput
	})
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// The table and log printers write straight to os.Stdout.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	runErr := fn()
	os.Stdout = previous
	_ = w.Close()
	return <-done, runErr
}

func TestFunctionsExposedRendersTheTable(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)

	out, err := captureStdout(t, func() error {
		return functionsExposedCmd.RunE(functionsExposedCmd, nil)
	})
	if err != nil {
		t.Fatalf("exposed: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); got != "GET /api/wf/slim/tool-exposures?format=json" {
		t.Fatalf("requests = %s", got)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	for i, want := range []string{
		"NAME  SERVER  MIN ROLE  ENABLED  REGISTERED",
		"get  http  viewer  true  true",
		"lookup_customer  org-worker  developer  false  false",
	} {
		if got := columnGap.ReplaceAllString(strings.TrimSpace(lines[i]), "  "); got != want {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

func TestFunctionsExposeSendsThePolicy(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	t.Cleanup(func() {
		_ = functionsExposeCmd.Flags().Set("min-role", "viewer")
		_ = functionsExposeCmd.Flags().Set("disabled", "false")
	})
	_ = functionsExposeCmd.Flags().Set("min-role", "admin")
	_ = functionsExposeCmd.Flags().Set("disabled", "true")

	out, err := captureStdout(t, func() error {
		return functionsExposeCmd.RunE(functionsExposeCmd, []string{"http", "get"})
	})
	if err != nil {
		t.Fatalf("expose: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); got != "PUT /api/wf/slim/tool-exposures/http/get?format=json" {
		t.Fatalf("requests = %s", got)
	}
	if len(stub.bodies) != 1 || stub.bodies[0]["min_role"] != "admin" || stub.bodies[0]["enabled"] != false {
		t.Fatalf("body = %v; want min_role admin, enabled false", stub.bodies)
	}
	// The printed result is the exposure itself, not the envelope around it.
	if !strings.Contains(out, "min_role: admin") || strings.Contains(out, "exposure:") {
		t.Errorf("expected the exposure row unwrapped, got:\n%s", out)
	}
}

func TestFunctionsExposeDefaultsToViewerAndEnabled(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	if _, err := captureStdout(t, func() error {
		return functionsExposeCmd.RunE(functionsExposeCmd, []string{"http", "get"})
	}); err != nil {
		t.Fatalf("expose: %v", err)
	}
	if stub.bodies[0]["min_role"] != "viewer" || stub.bodies[0]["enabled"] != true {
		t.Fatalf("body = %v; want the documented defaults", stub.bodies)
	}
}

func TestFunctionsExposeRejectsAnUnknownRoleLocally(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	t.Cleanup(func() { _ = functionsExposeCmd.Flags().Set("min-role", "viewer") })
	_ = functionsExposeCmd.Flags().Set("min-role", "member")
	err := functionsExposeCmd.RunE(functionsExposeCmd, []string{"http", "get"})
	if err == nil || !strings.Contains(err.Error(), "viewer, developer, admin, owner") {
		t.Fatalf("err = %v; want the role ladder", err)
	}
	if len(stub.requests) != 0 {
		t.Fatalf("a bad role reached the server: %v", stub.requests)
	}
}

func TestFunctionsExposeExplainsRefusals(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		want     string
		wantCode int
	}{
		{"exposure limit", 409, `{"error":"limit reached","code":"EXPOSURE_LIMIT"}`, "already exposes 100 functions; unexpose one first", 6},
		{"validation", 400, `{"error":"function names starting with _ cannot be exposed","code":"VALIDATION_FAILED"}`, "function names starting with _ cannot be exposed", 1},
		{"forbidden", 403, `{"error":"forbidden","code":"FORBIDDEN"}`, "requires the admin or owner role", 3},
		{"not registered", 404, `{"error":"not found","code":"NOT_FOUND"}`, "function http/get is not registered — check `dibbla fn list`", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &exposureStub{putStatus: tc.status, putBody: tc.body}
			useStub(t, stub)
			err := functionsExposeCmd.RunE(functionsExposeCmd, []string{"http", "get"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
			var coded interface{ ExitCode() int }
			if !errors.As(err, &coded) || coded.ExitCode() != tc.wantCode {
				t.Errorf("exit code for %d: got %v, want %d", tc.status, err, tc.wantCode)
			}
		})
	}
}

func TestFunctionsUnexposeWithYesDeletes(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	t.Cleanup(func() { _ = functionsUnexposeCmd.Flags().Set("yes", "false") })
	_ = functionsUnexposeCmd.Flags().Set("yes", "true")
	if err := functionsUnexposeCmd.RunE(functionsUnexposeCmd, []string{"http", "get"}); err != nil {
		t.Fatalf("unexpose: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); got != "DELETE /api/wf/slim/tool-exposures/http/get?format=json" {
		t.Fatalf("requests = %s", got)
	}
}

func TestFunctionsUnexposeWithoutYesRefusesNonInteractively(t *testing.T) {
	// Tests run with stdin that is not a terminal, which is the CI case the
	// confirmation exists for: no --yes, no request.
	stub := &exposureStub{}
	useStub(t, stub)
	if err := functionsUnexposeCmd.RunE(functionsUnexposeCmd, []string{"http", "get"}); err != nil {
		t.Fatalf("unexpose: %v", err)
	}
	if len(stub.requests) != 0 {
		t.Fatalf("deleted without confirmation: %v", stub.requests)
	}
}

func TestFunctionsInvocationsForwardsFiltersAndRendersTheTable(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	set := map[string]string{"server": "http", "function": "get", "source": "mcp", "user": "user-7", "since": "2026-09-01T00:00:00Z", "limit": "10"}
	t.Cleanup(func() {
		for k := range set {
			_ = functionsInvocationsCmd.Flags().Set(k, "")
		}
		_ = functionsInvocationsCmd.Flags().Set("limit", "50")
	})
	for k, v := range set {
		_ = functionsInvocationsCmd.Flags().Set(k, v)
	}

	out, err := captureStdout(t, func() error {
		return functionsInvocationsCmd.RunE(functionsInvocationsCmd, nil)
	})
	if err != nil {
		t.Fatalf("invocations: %v", err)
	}
	if len(stub.requests) != 1 {
		t.Fatalf("requests = %v", stub.requests)
	}
	for _, want := range []string{"format=json", "server=http", "function=get", "source=mcp", "user=user-7", "since=2026-09-01T00%3A00%3A00Z", "limit=10"} {
		if !strings.Contains(stub.requests[0], want) {
			t.Errorf("query missing %s: %s", want, stub.requests[0])
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "ID WHEN FUNCTION SOURCE USER STATUS DURATION ERROR" {
		t.Errorf("header = %q", got)
	}
	// A completed call has an empty ERROR cell; a failed one shows the
	// summary, not the code, when both exist. Durations are humanised.
	if !strings.Contains(lines[1], "inv-1") || !strings.Contains(lines[1], "http/get") || !strings.Contains(lines[1], "123ms") || strings.Contains(lines[1], "null") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], "1.5s") || !strings.Contains(lines[2], "upstream said no") || strings.Contains(lines[2], "UPSTREAM_FAILED") {
		t.Errorf("row 2 = %q", lines[2])
	}
}

func TestFunctionsInvocationWithLogsStreamsTheInvocationLogs(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	t.Cleanup(func() { _ = functionsInvocationCmd.Flags().Set("logs", "false") })
	_ = functionsInvocationCmd.Flags().Set("logs", "true")

	out, err := captureStdout(t, func() error {
		return functionsInvocationCmd.RunE(functionsInvocationCmd, []string{"inv-1"})
	})
	if err != nil {
		t.Fatalf("invocation --logs: %v", err)
	}
	if len(stub.requests) != 2 || !strings.HasPrefix(stub.requests[1], "GET /api/wf/slim/tool-invocations/inv-1/logs?") {
		t.Fatalf("requests = %v; want the record then its logs", stub.requests)
	}
	// The stream is a backfill from just before the call started, at every
	// level — an invocation is over by the time anyone looks.
	if !strings.Contains(stub.requests[1], "level=debug") || !strings.Contains(stub.requests[1], "since=2026-09-07T09%3A59%3A00Z") || strings.Contains(stub.requests[1], "follow=") {
		t.Errorf("logs query = %s", stub.requests[1])
	}
	if !strings.Contains(out, "status: completed") {
		t.Errorf("record missing from output:\n%s", out)
	}
	if !strings.Contains(out, "calling http get") || !strings.Contains(out, "run completed") {
		t.Errorf("log lines missing from output:\n%s", out)
	}
}

func TestFunctionsInvocationNotFound(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	err := functionsInvocationCmd.RunE(functionsInvocationCmd, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), `invocation "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
}
