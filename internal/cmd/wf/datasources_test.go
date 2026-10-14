package wf

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// The data source commands (DIB-1440) are thin over the slim API that the
// platform_data_sources MCP tool also calls, so what is worth pinning is the
// wire — the request each one sends, with source "cli" — and what the caller
// sees when the server refuses or cuts the answer.

type dataSourceStub struct {
	mu       sync.Mutex
	requests []string
	bodies   []map[string]interface{}
	sources  []string
}

func useDataSourceStub(t *testing.T, stub *dataSourceStub) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		stub.requests = append(stub.requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/wf/slim/data-sources":
			_, _ = w.Write([]byte(`{"data_sources":[
				{"id":"6f1c","source_id":"voicelines","display_name":"Voice lines","description":"Voice lines of the game characters","capabilities":["semantic_search"],"filters":{"speaker":"string","mood":"string"},"min_role":"viewer"},
				{"id":"7a2d","source_id":"tickets","display_name":"Tickets","capabilities":[],"min_role":"developer"}
			],"count":2}`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/wf/slim/data-sources/"):
			raw, _ := io.ReadAll(r.Body)
			var body map[string]interface{}
			_ = json.Unmarshal(raw, &body)
			stub.bodies = append(stub.bodies, body)
			stub.sources = append(stub.sources, r.Header.Get("X-Dibbla-Source"))
			switch r.URL.Path {
			case "/api/wf/slim/data-sources/hidden/query":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Data source not found","code":"NOT_FOUND"}`))
			case "/api/wf/slim/data-sources/admins-only/query":
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"this data source requires the developer role or higher","code":"FORBIDDEN"}`))
			case "/api/wf/slim/data-sources/voicelines/query":
				if f, _ := body["filters"].(map[string]interface{}); f["character"] != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"cannot filter on character: filterable columns are mood, speaker","code":"VALIDATION_FAILED","details":[{"field":"filters.character","error":"not_filterable"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"invocation_id":"inv-1","source_id":"voicelines","operation":"semantic_search","status":"completed","top_k":5,"count":1,"truncated":false,
					"results":[{"id":"2","title":"mario","metadata":{"speaker":"mario","line":"Grr, Bowser!","mood":"angry","_score":0.93}}]}`))
			case "/api/wf/slim/data-sources/long/query":
				_, _ = w.Write([]byte(`{"invocation_id":"inv-2","status":"completed","count":1,"truncated":true,
					"truncation_note":"Response cut to fit 16 KiB: values longer than 500 characters were shortened and 1 of 2 results returned.",
					"results":[{"id":"1","metadata":{"line":"aaa… [truncated]"}}]}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found","code":"NOT_FOUND"}`))
		}
	}))
	t.Cleanup(srv.Close)
	previous := apiClient
	apiClient = apiclient.NewClient(srv.URL, "test-token", false)
	previousOutput := flagOutput
	flagOutput = ""
	t.Cleanup(func() {
		apiClient = previous
		flagOutput = previousOutput
	})
}

// resetQueryFlags puts the query command's flags back, since cobra keeps
// them between RunE calls in one test binary.
func resetQueryFlags(t *testing.T) {
	t.Cleanup(func() {
		for _, name := range []string{"filter", "top-k", "target", "min-score"} {
			f := dataSourcesQueryCmd.Flags().Lookup(name)
			if sv, ok := f.Value.(interface{ Replace([]string) error }); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	})
}

func TestDataSourceListRendersSourcesWithTheirFilters(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)

	out, err := captureStdout(t, func() error {
		return dataSourcesListCmd.RunE(dataSourcesListCmd, nil)
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); got != "GET /api/wf/slim/data-sources?format=json" {
		t.Fatalf("requests = %s", got)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	for i, want := range []string{
		"ID  NAME  MIN ROLE  FILTERS  DESCRIPTION",
		"voicelines  Voice lines  viewer  mood (string), speaker (string)  Voice lines of the game characters",
		"tickets  Tickets  developer  -",
	} {
		if got := columnGap.ReplaceAllString(strings.TrimSpace(lines[i]), "  "); got != want {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

func TestDataSourceQuerySendsQuestionFiltersAndTheCLISource(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)
	resetQueryFlags(t)
	_ = dataSourcesQueryCmd.Flags().Set("filter", "speaker=mario")
	_ = dataSourcesQueryCmd.Flags().Set("filter", "mood=angry")
	_ = dataSourcesQueryCmd.Flags().Set("filter", "mood=grumpy")
	_ = dataSourcesQueryCmd.Flags().Set("top-k", "5")

	out, err := captureStdout(t, func() error {
		return dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{"voicelines", "an angry line"})
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := strings.Join(stub.requests, ", "); got != "POST /api/wf/slim/data-sources/voicelines/query?format=json" {
		t.Fatalf("requests = %s", got)
	}
	if strings.Join(stub.sources, ",") != "cli" {
		t.Fatalf("X-Dibbla-Source = %v, want cli", stub.sources)
	}
	body := stub.bodies[0]
	if body["query"] != "an angry line" || body["top_k"] != float64(5) {
		t.Fatalf("body = %v", body)
	}
	filters, _ := body["filters"].(map[string]interface{})
	mood, _ := filters["mood"].([]interface{})
	if filters["speaker"] != "mario" || len(mood) != 2 || mood[0] != "angry" || mood[1] != "grumpy" {
		t.Fatalf("filters = %v; want speaker as a value and mood as a list", filters)
	}
	if !strings.Contains(out, "Grr, Bowser!") || !strings.Contains(out, "invocation_id: inv-1") {
		t.Errorf("expected the hit and its invocation, got:\n%s", out)
	}
}

func TestDataSourceQueryWithoutOptionsSendsOnlyTheQuestion(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)
	resetQueryFlags(t)
	if _, err := captureStdout(t, func() error {
		return dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{"voicelines", "an angry line"})
	}); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(stub.bodies[0]) != 1 {
		t.Fatalf("body = %v; want only the query, so the server's defaults apply", stub.bodies[0])
	}
}

func TestDataSourceQueryRefusesTopKAboveTheCapLocally(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)
	resetQueryFlags(t)
	_ = dataSourcesQueryCmd.Flags().Set("top-k", "50")

	err := dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{"voicelines", "an angry line"})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 20") {
		t.Fatalf("want the top-k cap explained, got %v", err)
	}
	if len(stub.requests) != 0 {
		t.Errorf("a refused query must not reach the server, sent %v", stub.requests)
	}
}

func TestDataSourceQueryRefusesAMalformedFilter(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)
	resetQueryFlags(t)
	_ = dataSourcesQueryCmd.Flags().Set("filter", "mario")

	err := dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{"voicelines", "an angry line"})
	if err == nil || !strings.Contains(err.Error(), "column=value") {
		t.Fatalf("want the filter syntax explained, got %v", err)
	}
	if len(stub.requests) != 0 {
		t.Errorf("a refused query must not reach the server, sent %v", stub.requests)
	}
}

func TestDataSourceQueryExplainsRefusals(t *testing.T) {
	cases := []struct {
		name, id, filter string
		status           int
		want             []string
	}{
		{"not queryable", "hidden", "", 404, []string{`"hidden" is not queryable`, "not exposed", "dibbla datasource list"}},
		{"role below min role", "admins-only", "", 403, []string{"requires the developer role", "min role"}},
		{"undeclared filter", "voicelines", "character=mario", 400, []string{"cannot filter on character", "filterable columns are mood, speaker"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &dataSourceStub{}
			useDataSourceStub(t, stub)
			resetQueryFlags(t)
			if tc.filter != "" {
				_ = dataSourcesQueryCmd.Flags().Set("filter", tc.filter)
			}
			_, err := captureStdout(t, func() error {
				return dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{tc.id, "an angry line"})
			})
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not say %q", err, w)
				}
			}
			var exit *exitError
			if !errors.As(err, &exit) || exit.code != apiclient.ExitCodeForStatus(tc.status) {
				t.Errorf("want the exit code for %d, got %v", tc.status, err)
			}
		})
	}
}

func TestDataSourceQueryMarksATruncatedAnswer(t *testing.T) {
	stub := &dataSourceStub{}
	useDataSourceStub(t, stub)
	resetQueryFlags(t)

	stderr := captureStderr(t, func() {
		out, err := captureStdout(t, func() error {
			return dataSourcesQueryCmd.RunE(dataSourcesQueryCmd, []string{"long", "anything"})
		})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if !strings.Contains(out, "truncated: true") {
			t.Errorf("the answer itself must keep truncated: true, got:\n%s", out)
		}
	})
	if !strings.Contains(stderr, "Truncated: Response cut to fit 16 KiB") {
		t.Errorf("want the truncation note on stderr, got %q", stderr)
	}
}

func TestFunctionsInvocationsDoesNotAskForDataSourceQueries(t *testing.T) {
	stub := &exposureStub{}
	useStub(t, stub)
	if _, err := captureStdout(t, func() error {
		return functionsInvocationsCmd.RunE(functionsInvocationsCmd, nil)
	}); err != nil {
		t.Fatalf("invocations: %v", err)
	}
	// The server lists function calls only unless subject_kind says
	// otherwise (DIB-1437); the CLI must never widen that.
	for _, r := range stub.requests {
		if strings.Contains(r, "subject_kind") {
			t.Fatalf("fn invocations sent %s; data source queries must stay out of it", r)
		}
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote;
// notes like the truncation line go there, not into the answer.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() { os.Stderr = previous }()
	fn()
	_ = w.Close()
	return <-done
}
