package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

func maintenanceRunDoc(id, status, terminalCode string) map[string]any {
	doc := map[string]any{
		"execution_id": id, "organization_id": "org_1", "deployment_id": "dep_1",
		"deployment_alias": "myapp", "trigger": "manual", "mode": "nightly",
		"status": status, "run_id": "run_" + id, "model": "dibbla/test",
		"deadline_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"created_at":  "2026-08-31T10:00:00Z", "evidence_refs": []string{},
	}
	if terminalCode != "" {
		doc["terminal_code"] = terminalCode
	}
	return doc
}

func maintenanceStatusDoc() map[string]any {
	return map[string]any{
		"alias": "myapp", "deployment_id": "dep_1",
		"settings": map[string]any{
			"enabled": false, "app_version": 7, "org_version": 3,
			"model": "dibbla/test", "nightly_cron": "0 3 * * *", "timezone": "UTC",
		},
	}
}

func dispatchDoc(code, executionID string, replayed bool) map[string]any {
	return map[string]any{
		"code": code, "execution_id": executionID, "run_id": "run_" + executionID,
		"reservation_id": "res_1", "status": "running", "replayed": replayed,
	}
}

func fastMaintenancePolling(t *testing.T) {
	t.Helper()
	oldInterval, oldTimeout := maintenancePollInterval, maintenancePollTimeout
	maintenancePollInterval, maintenancePollTimeout = 2*time.Millisecond, time.Minute
	t.Cleanup(func() { maintenancePollInterval, maintenancePollTimeout = oldInterval, oldTimeout })
}

func TestRunAppsMaintenanceStatusCoreJSONPreservesServerFields(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/maintenance-agent": func(w http.ResponseWriter, _ *http.Request) {
			doc := maintenanceStatusDoc()
			doc["future_field"] = "kept"
			writeJSON(w, http.StatusOK, doc)
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsMaintenanceStatusCore(&stdout, &stderr, api.url(), "tok", "myapp", true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["future_field"] != "kept" {
		t.Fatalf("raw API field lost: %s", stdout.String())
	}
}

func TestRunAppsMaintenanceStatusDoesNotMislabelCapability404AsMissingApp(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/maintenance-agent": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, errEnvelope("MAINTENANCE_AGENT_NOT_FOUND", "maintenance is unavailable"))
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsMaintenanceStatusCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if strings.Contains(stderr.String(), "apps list") {
		t.Fatalf("capability 404 received misleading alias hint: %s", stderr.String())
	}
}

func TestRunAppsMaintenanceChangeUsesAppVersion(t *testing.T) {
	var put map[string]any
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/maintenance-agent": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, maintenanceStatusDoc())
		},
		"PUT /api/deploy/deployments/myapp/maintenance-agent": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
				t.Fatal(err)
			}
			writeJSON(w, http.StatusOK, map[string]any{"alias": "myapp", "deployment_id": "dep_1", "enabled": true})
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsMaintenanceChangeCore(&stdout, &stderr, api.url(), "tok", "myapp", true, true, true,
		func(string) (bool, error) { t.Fatal("--yes must skip prompt"); return false, nil })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if put["enabled"] != true || put["version"] != float64(7) {
		t.Fatalf("PUT must carry enabled + server app_version: %#v", put)
	}
}

func TestRunAppsMaintenanceRunFindingRecordedJSON(t *testing.T) {
	fastMaintenancePolling(t)
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/maintenance-agent/runs": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Idempotency-Key") != "intent-1" {
				t.Fatalf("idempotency key = %q", r.Header.Get("Idempotency-Key"))
			}
			writeJSON(w, http.StatusCreated, dispatchDoc("dispatched", "mex_1", false))
		},
		"GET /api/deploy/deployments/myapp/maintenance-agent/runs/mex_1": func(w http.ResponseWriter, _ *http.Request) {
			doc := maintenanceRunDoc("mex_1", "completed", "FINDING_RECORDED")
			doc["summary"] = "A bounded finding"
			doc["fingerprint"] = "fp_1"
			doc["evidence_refs"] = []string{"ev_log_1"}
			doc["proposal_id"] = "pr_0123456789abcdef0123"
			writeJSON(w, http.StatusOK, doc)
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsMaintenanceRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "nightly", "", "intent-1", runModeSync, true, time.Now)
	if code != exitMaintenanceFindingRecorded {
		t.Fatalf("exit %d, want %d: %s", code, exitMaintenanceFindingRecorded, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("one JSON document required: %v\n%s", err, stdout.String())
	}
	if doc["outcome"] != "finding_recorded" || doc["exit_code"] != float64(11) {
		t.Fatalf("typed terminal mismatch: %#v", doc)
	}
	exec := doc["execution"].(map[string]any)
	if exec["proposal_id"] != "pr_0123456789abcdef0123" || exec["fingerprint"] != "fp_1" {
		t.Fatalf("observation receipt lost: %#v", exec)
	}
}

func TestRunAppsMaintenanceRunFollowReplayEndsInOneSummary(t *testing.T) {
	fastMaintenancePolling(t)
	reads := 0
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/maintenance-agent/runs": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, dispatchDoc("replayed", "mex_same", true))
		},
		"GET /api/deploy/deployments/myapp/maintenance-agent/runs/mex_same": func(w http.ResponseWriter, _ *http.Request) {
			reads++
			if reads == 1 {
				writeJSON(w, http.StatusOK, maintenanceRunDoc("mex_same", "running", ""))
				return
			}
			doc := maintenanceRunDoc("mex_same", "completed", "NO_FINDING")
			doc["deduplicated"] = true
			doc["summary"] = "Known fingerprint revalidated"
			writeJSON(w, http.StatusOK, doc)
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsMaintenanceRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "nightly", "", "same-key", runModeFollow, true, time.Now)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("created + terminal status + summary required, got %d:\n%s", len(lines), stdout.String())
	}
	summaries := 0
	for i, line := range lines {
		var doc map[string]any
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if doc["type"] == "summary" {
			summaries++
			if doc["outcome"] != "found_nothing" || doc["exit_code"] != float64(0) {
				t.Fatalf("terminal summary wrong: %#v", doc)
			}
		}
		if i == 0 {
			dispatch := doc["dispatch"].(map[string]any)
			if dispatch["replayed"] != true || dispatch["execution_id"] != "mex_same" {
				t.Fatalf("replay receipt missing: %#v", doc)
			}
		}
	}
	if summaries != 1 {
		t.Fatalf("summary count %d", summaries)
	}
}

func TestMaintenanceTerminalOutcomeAndExitContract(t *testing.T) {
	tests := []struct {
		name, status, terminalCode, outcome string
		exitCode                            int
	}{
		{"found nothing", "completed", "NO_FINDING", "found_nothing", 0},
		{"proposal", "completed", "PROPOSAL_CREATED", "proposed", 0},
		{"finding", "completed", "FINDING_RECORDED", "finding_recorded", 11},
		{"budget", "budget_exhausted", "BUDGET_LIMIT_REACHED", "budget_exhausted", 0},
		{"skipped", "skipped_concurrent", "", "skipped", 0},
		{"required tool failed", "completed", "REQUIRED_TOOL_FAILED", "assessment_blocked", 1},
		{"required tool missing", "completed", "REQUIRED_TOOL_MISSING", "assessment_blocked", 1},
		{"model timeout", "completed", "MODEL_TIMEOUT", "run_error", 1},
		{"storage error", "error", "", "run_error", 1},
		{"unknown code", "completed", "FUTURE_RESULT", "unknown_outcome", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := maintenanceRunDoc("mex_contract", tt.status, tt.terminalCode)
			encoded, _ := json.Marshal(run)
			var execution apps.MaintenanceRun
			if err := json.Unmarshal(encoded, &execution); err != nil {
				t.Fatal(err)
			}
			if got := maintenanceOutcome(&execution); got != tt.outcome {
				t.Fatalf("outcome %q, want %q", got, tt.outcome)
			}
			if got := maintenanceExitCode(&execution); got != tt.exitCode {
				t.Fatalf("exit %d, want %d", got, tt.exitCode)
			}
		})
	}
}

func TestRunAppsMaintenanceRunDistinctExitCodes(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusUnauthorized, 3},
		{http.StatusForbidden, 3},
		{http.StatusBadRequest, 5},
		{http.StatusConflict, 6},
		{http.StatusInternalServerError, 1},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("http_%d", tc.status), func(t *testing.T) {
			api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
				"POST /api/deploy/deployments/myapp/maintenance-agent/runs": func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, tc.status, errEnvelope("TYPED_REFUSAL", "refused"))
				},
			})
			var stdout, stderr bytes.Buffer
			got := runAppsMaintenanceRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "nightly", "", "key", runModeAsync, true, time.Now)
			if got != tc.want {
				t.Fatalf("exit %d, want %d", got, tc.want)
			}
			if !strings.Contains(stderr.String(), "TYPED_REFUSAL") {
				t.Fatalf("server code lost: %s", stderr.String())
			}
		})
	}
}

func TestRunAppsMaintenanceRunRejectsInvalidModeWithoutRequest(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{})
	var stdout, stderr bytes.Buffer
	if code := runAppsMaintenanceRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "wrong", "", "key", runModeAsync, false, time.Now); code != 5 {
		t.Fatalf("exit %d", code)
	}
	if api.total() != 0 {
		t.Fatalf("invalid local input made %d requests", api.total())
	}
}

func TestRunAppsMaintenanceRunTranslatesDocumentedCheckTriageMode(t *testing.T) {
	var body map[string]any
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/maintenance-agent/runs": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			writeJSON(w, http.StatusCreated, dispatchDoc("dispatched", "mex_triage", false))
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsMaintenanceRunCore(&stdout, &stderr, api.url(), "tok", "myapp", "check-triage", "acr_1", "key", runModeAsync, true, time.Now)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if body["mode"] != "check_triage" || body["check_run_id"] != "acr_1" {
		t.Fatalf("API body = %#v", body)
	}
}
