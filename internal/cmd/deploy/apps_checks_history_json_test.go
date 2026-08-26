package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Negative control for `apps checks history --json` without --check (DIB-460).
//
// There is no app-wide history endpoint, so that path merges one page per
// check. It used to re-serialise the merged runs through apps.CheckRun — a
// hand-maintained mirror of deploy-api's wire view that had drifted in BOTH
// directions. It dropped six fields the server sends (evidence_refs,
// evidence_gaps, execution_id, transport_status, assertion_status,
// check_fingerprint) and invented seven the server never sends, as zero
// values. Two of those invented values are measurements: a run still in flight
// came out carrying finished_at "0001-01-01T00:00:00Z" and duration_ms 0 —
// a date in year 1 and a run that took no time, which is exactly why
// deploy-api's view omits both rather than sending zeroes
// (application_check_history_view.go:69-73).
//
// A dropped field is an absence and somebody eventually notices. An invented
// measurement is worse: it reads as a reading that was taken.
//
// These fixtures are the server's documents, so the tests can assert the CLI
// hands them back rather than rebuilding them. Every test in this file fails
// against the pre-fix CLI; see state/impl0074-notes.md in dibbla-implementations
// for both runs.

// A finished run carrying real evidence. evidence_refs/evidence_gaps are JSONB
// columns the server passes through as raw JSON, so they are arrays of objects
// here, not strings — a fixture with empty arrays could not tell a working
// pass-through from a struct that drops them.
const historyRunFinished = `{
  "schema_version": 1,
  "run_id": "acr_home_2",
  "execution_id": "ace_home_2",
  "check_id": "home-page",
  "deployment_id": "dep_1",
  "deployment_alias": "myapp",
  "outcome": "fail",
  "code": "ASSERTION_FAILED",
  "summary": "Expected citations, received none",
  "transport_status": "200",
  "assertion_status": "failed",
  "fingerprint": "fp_home_2",
  "check_fingerprint": "cfp_home",
  "transition_id": "act_9",
  "tool_calls": 3,
  "tokens": {"cache_read": 120, "cache_write": 40, "total": 900},
  "evidence_refs": [
    {"kind": "http", "step": 1, "url": "https://myapp.dibbla.com/", "status": 200},
    {"kind": "screenshot", "step": 2, "object": "evidence/acr_home_2/step-2.png"}
  ],
  "evidence_gaps": [
    {"kind": "screenshot", "step": 3, "reason": "capture_unavailable"}
  ],
  "started_at": "2026-08-25T11:00:00Z",
  "finished_at": "2026-08-25T11:00:42Z",
  "duration_ms": 42000
}`

// A run still in flight. The server OMITS finished_at and duration_ms here
// rather than sending zero values; that omission is the thing under test.
const historyRunInFlight = `{
  "schema_version": 1,
  "run_id": "acr_assist_1",
  "execution_id": "ace_assist_1",
  "check_id": "assistant-answer",
  "deployment_id": "dep_1",
  "deployment_alias": "myapp",
  "outcome": "",
  "tool_calls": 0,
  "tokens": {"cache_read": 0, "cache_write": 0, "total": 0},
  "started_at": "2026-08-25T10:30:00Z"
}`

// A finished run carrying a field this CLI has never heard of. A pass-through
// keeps it; any struct mirror silently eats it, which is the mechanism that
// produced this bug in the first place.
const historyRunFutureField = `{
  "schema_version": 1,
  "run_id": "acr_assist_0",
  "execution_id": "ace_assist_0",
  "check_id": "assistant-answer",
  "deployment_id": "dep_1",
  "deployment_alias": "myapp",
  "outcome": "pass",
  "summary": "grounded in the retrieved sources",
  "tool_calls": 1,
  "tokens": {"cache_read": 10, "cache_write": 5, "total": 42},
  "evidence_refs": [{"kind": "assertion", "step": 1, "passed": true}],
  "started_at": "2026-08-25T09:00:00Z",
  "finished_at": "2026-08-25T09:00:07Z",
  "duration_ms": 7000,
  "verdict_confidence": 0.93
}`

// historyAPI serves the two checks and their pages, with the runs above split
// across them so the merge path really has to merge.
func historyAPI(t *testing.T) *fakeChecksAPI {
	t.Helper()
	return newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/application-checks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": 1, "configured": true,
				"definitions": []map[string]any{
					{"id": "home-page", "kind": "http"},
					{"id": "assistant-answer", "kind": "semantic"},
				},
			})
		},
		"GET /api/deploy/deployments/myapp/application-checks/home-page/runs": rawRunsPage(historyRunFinished),
		"GET /api/deploy/deployments/myapp/application-checks/assistant-answer/runs": rawRunsPage(
			historyRunInFlight, historyRunFutureField),
	})
}

// rawRunsPage writes a runs page from raw run documents, so the bytes the
// server sends are the bytes written here — no round-trip through a Go type
// on the fixture side either.
func rawRunsPage(runs ...string) http.HandlerFunc {
	body := []byte(`{"runs":[`)
	for i, run := range runs {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, run...)
	}
	body = append(body, `],"next_cursor":""}`...)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// mergedHistoryRuns runs the merge path with --json and returns the emitted run
// documents keyed by run_id, plus the order they came out in.
func mergedHistoryRuns(t *testing.T) (map[string]map[string]any, []string) {
	t.Helper()
	api := historyAPI(t)
	now := func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "", 0, 0, true, now); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var doc struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	got := map[string]map[string]any{}
	var order []string
	for _, raw := range doc.Runs {
		var run map[string]any
		if err := json.Unmarshal(raw, &run); err != nil {
			t.Fatalf("decode run: %v\n%s", err, raw)
		}
		id, _ := run["run_id"].(string)
		if id == "" {
			t.Fatalf("run with no run_id: %s", raw)
		}
		got[id] = run
		order = append(order, id)
	}
	return got, order
}

func decodeRun(t *testing.T, doc string) map[string]any {
	t.Helper()
	var run map[string]any
	if err := json.Unmarshal([]byte(doc), &run); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return run
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The headline defect (A7): evidence_refs and evidence_gaps are real stored
// columns that the merge path threw away without an error or a warning.
func TestRunAppsChecksHistoryCore_MergedJSONKeepsEvidenceFields(t *testing.T) {
	got, _ := mergedHistoryRuns(t)

	run, ok := got["acr_home_2"]
	if !ok {
		t.Fatalf("acr_home_2 missing from output; got %v", sortedKeys(got["acr_home_2"]))
	}
	want := decodeRun(t, historyRunFinished)

	for _, field := range []string{"evidence_refs", "evidence_gaps"} {
		if _, present := run[field]; !present {
			t.Errorf("%s was dropped — the server sent it and it is not in the output. keys: %v",
				field, sortedKeys(run))
			continue
		}
		if !reflect.DeepEqual(run[field], want[field]) {
			t.Errorf("%s changed in transit:\n got %#v\nwant %#v", field, run[field], want[field])
		}
	}

	// A run with no gaps must still keep its refs.
	if other := got["acr_assist_0"]; other != nil {
		if _, present := other["evidence_refs"]; !present {
			t.Errorf("acr_assist_0 lost evidence_refs; keys: %v", sortedKeys(other))
		}
	}
}

// A run in flight must not come back carrying measurements nobody took. This
// is the invented half of the defect, and it is the worse half.
func TestRunAppsChecksHistoryCore_MergedJSONInventsNoMeasurements(t *testing.T) {
	got, _ := mergedHistoryRuns(t)

	run, ok := got["acr_assist_1"]
	if !ok {
		t.Fatal("acr_assist_1 (the in-flight run) missing from output")
	}
	for _, absent := range []string{"finished_at", "duration_ms"} {
		if value, present := run[absent]; present {
			t.Errorf("%s was invented for a run that has not finished: %v — the server omits it on purpose",
				absent, value)
		}
	}
	// Fields the CLI's old mirror carried and deploy-api never sends. Emitting
	// them as zero values tells a reader the server answered when it did not.
	for _, invented := range []string{
		"workflow_run_id", "config_revision", "target_deployment_revision",
		"target_image_digests", "trigger",
	} {
		if value, present := run[invented]; present {
			t.Errorf("%s is not a field deploy-api sends, but the output has it: %v", invented, value)
		}
	}
	tokens, _ := run["tokens"].(map[string]any)
	for _, invented := range []string{"input", "output"} {
		if value, present := tokens[invented]; present {
			t.Errorf("tokens.%s is not a field deploy-api sends, but the output has it: %v", invented, value)
		}
	}
}

// The general form of both defects: every merged run must be the server's own
// document. This is the assertion that keeps holding when deploy-api adds its
// next field.
func TestRunAppsChecksHistoryCore_MergedJSONIsTheServerDocument(t *testing.T) {
	got, order := mergedHistoryRuns(t)

	want := map[string]map[string]any{
		"acr_home_2":   decodeRun(t, historyRunFinished),
		"acr_assist_1": decodeRun(t, historyRunInFlight),
		"acr_assist_0": decodeRun(t, historyRunFutureField),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d runs, got %d: %v", len(want), len(got), order)
	}
	// Newest first, across both checks.
	if wantOrder := []string{"acr_home_2", "acr_assist_1", "acr_assist_0"}; !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("order: got %v want %v", order, wantOrder)
	}
	for id, wantRun := range want {
		gotRun, ok := got[id]
		if !ok {
			t.Errorf("%s missing from output", id)
			continue
		}
		if reflect.DeepEqual(gotRun, wantRun) {
			continue
		}
		for _, key := range sortedKeys(wantRun) {
			gotValue, present := gotRun[key]
			if !present {
				t.Errorf("%s: %q dropped (server sent %#v)", id, key, wantRun[key])
			} else if !reflect.DeepEqual(gotValue, wantRun[key]) {
				t.Errorf("%s: %q changed: got %#v want %#v", id, key, gotValue, wantRun[key])
			}
		}
		for _, key := range sortedKeys(gotRun) {
			if _, present := wantRun[key]; !present {
				t.Errorf("%s: %q invented (server never sent it): %#v", id, key, gotRun[key])
			}
		}
	}
}

// Pass-through, not a faithfully-updated mirror.
//
// Do not delete this as over-strict. The three tests above assert a STATE — the
// right fields came out — and a re-modelled apps.CheckRun would satisfy all
// three on the day it was written, then drift again the day deploy-api adds its
// next field, which is precisely how this bug was born. This test asserts the
// CHOICE instead: key order survives carrying the server's bytes and does not
// survive a Go struct, so it is the cheapest available proof of which of the
// two fixes is actually in place. If it ever fails, the question to ask is not
// "how do I make this pass" but "did someone reintroduce the mirror".
func TestRunAppsChecksHistoryCore_MergedJSONPassesThroughRatherThanRebuilding(t *testing.T) {
	api := historyAPI(t)
	now := func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }

	var stdout, stderr bytes.Buffer
	if code := runAppsChecksHistoryCore(&stdout, &stderr, api.url(), "tok", "myapp", "", 0, 0, true, now); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var doc struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if len(doc.Runs) == 0 {
		t.Fatal("no runs in output")
	}
	// doc.Runs[0] is acr_home_2 (newest).
	if got, want := jsonKeyOrder(t, doc.Runs[0]), jsonKeyOrder(t, []byte(historyRunFinished)); !reflect.DeepEqual(got, want) {
		t.Errorf("run document was rebuilt, not passed through.\n got %v\nwant %v", got, want)
	}
}

// jsonKeyOrder returns the top-level keys of a JSON object in the order they
// appear on the wire.
func jsonKeyOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("not a JSON object: %s", raw)
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatalf("key token: %v", err)
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}
