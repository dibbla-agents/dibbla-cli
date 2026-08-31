package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testProposalID = "pr_0123456789abcdef0123"

func proposalReadDoc() map[string]any {
	return map[string]any{
		"id": testProposalID, "deployment_alias": "myapp", "title": "Fix health route",
		"description": "The route returned 500", "status": "ready",
		"author_id": "maintenance-agent:dep_1", "author_name": "Dibbla Maintenance Agent",
		"governance_model": "admin_four_eyes", "governance_version": 4,
		"base_sha": strings.Repeat("a", 40), "head_sha": strings.Repeat("b", 40),
		"source_ref":   "refs/dibbla/proposals/" + testProposalID + "/head",
		"options_hash": "hash", "source": "maintenance_agent", "risk": "low",
		"maintenance_execution_id": "mex_1",
		"evidence":                 map[string]any{"refs": []string{"ev_log_1"}},
		"events":                   []map[string]any{{"id": 1, "proposal_id": testProposalID, "type": "created", "actor_id": "maintenance-agent:dep_1", "created_at": "2026-08-31T10:00:00Z"}},
		"decision": map[string]any{
			"can_decide": true, "eligible_reviewer": true, "reason": "eligible",
			"message": "you can approve or deny this proposal", "required_role": "owner_or_admin_other_than_author",
		},
		"created_at": "2026-08-31T10:00:00Z", "updated_at": "2026-08-31T10:01:00Z",
	}
}

func proposalDiffDoc(patch string) map[string]any {
	return map[string]any{
		"source": "maintenance_agent", "risk": "low",
		"evidence": map[string]any{"summary": "Observed a 500", "refs": []string{"ev_log_1"}},
		"diff": map[string]any{
			"base_sha": strings.Repeat("a", 40), "head_sha": strings.Repeat("b", 40),
			"files":     []map[string]any{{"path": "main.go", "status": "modified", "additions": 1, "deletions": 1, "patch": patch}},
			"additions": 1, "deletions": 1, "truncated": false, "total_files": 1,
		},
	}
}

func TestRunAppsProposalsShowDiffJSONPreservesAPIDocuments(t *testing.T) {
	patch := "@@ -1 +1 @@\n-old\n+new\n"
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/proposals/" + testProposalID: func(w http.ResponseWriter, _ *http.Request) {
			doc := proposalReadDoc()
			doc["future_proposal_field"] = "kept"
			writeJSON(w, http.StatusOK, doc)
		},
		"GET /api/deploy/deployments/myapp/proposals/" + testProposalID + "/diff": func(w http.ResponseWriter, _ *http.Request) {
			doc := proposalDiffDoc(patch)
			doc["future_diff_field"] = "kept"
			writeJSON(w, http.StatusOK, doc)
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsProposalsShowCore(&stdout, &stderr, api.url(), "tok", "myapp", testProposalID, true, true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var doc struct {
		Type     string         `json:"type"`
		Proposal map[string]any `json:"proposal"`
		Diff     map[string]any `json:"diff"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode combined document: %v\n%s", err, stdout.String())
	}
	if doc.Type != "proposal_review" || doc.Proposal["future_proposal_field"] != "kept" || doc.Diff["future_diff_field"] != "kept" {
		t.Fatalf("API documents drifted: %#v", doc)
	}
	if doc.Proposal["status"] != "ready" || doc.Proposal["author_id"] != "maintenance-agent:dep_1" {
		t.Fatalf("actor/status missing: %#v", doc.Proposal)
	}
	diff := doc.Diff["diff"].(map[string]any)
	files := diff["files"].([]any)
	if files[0].(map[string]any)["patch"] != patch {
		t.Fatalf("diff bytes changed: %#v", files[0])
	}
	evidence := doc.Diff["evidence"].(map[string]any)
	if evidence["summary"] != "Observed a 500" {
		t.Fatalf("evidence changed: %#v", evidence)
	}
}

func TestRunAppsProposalsShowRendersServerDecision(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/proposals/" + testProposalID: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, proposalReadDoc())
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsProposalsShowCore(&stdout, &stderr, api.url(), "tok", "myapp", testProposalID, false, false); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"ready", "maintenance-agent:dep_1", "eligible", "you can approve or deny this proposal"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing server field %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunAppsProposalActionPostsDirectlyWithoutClientApprovalState(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/proposals/" + testProposalID + "/approve": func(w http.ResponseWriter, _ *http.Request) {
			doc := proposalReadDoc()
			doc["status"] = "queued"
			doc["decision_by"] = "user_reviewer"
			writeJSON(w, http.StatusAccepted, doc)
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsProposalActionCore(&stdout, &stderr, api.url(), "tok", "myapp", testProposalID, "approve", true, true,
		func(string) (bool, error) { t.Fatal("--yes must skip prompt"); return false, nil })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if api.total() != 1 || api.hit("POST /api/deploy/deployments/myapp/proposals/"+testProposalID+"/approve") != 1 {
		t.Fatalf("action must call only the decision endpoint; hits=%d", api.total())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "queued" || got["decision_by"] != "user_reviewer" {
		t.Fatalf("server actor/status lost: %#v", got)
	}
}

func TestRunAppsProposalActionMapsServerConflict(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"POST /api/deploy/deployments/myapp/proposals/" + testProposalID + "/retry": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusConflict, errEnvelope("PROPOSAL_NOT_READY", "proposal is not deploy_failed"))
		},
	})
	var stdout, stderr bytes.Buffer
	code := runAppsProposalActionCore(&stdout, &stderr, api.url(), "tok", "myapp", testProposalID, "retry", true, false,
		func(string) (bool, error) { return true, nil })
	if code != 6 {
		t.Fatalf("exit %d, want conflict 6", code)
	}
	if !strings.Contains(stderr.String(), "PROPOSAL_NOT_READY") {
		t.Fatalf("typed conflict lost: %s", stderr.String())
	}
}

func TestRunAppsProposalsListEmptyIsSuccess(t *testing.T) {
	api := newFakeChecksAPI(t, map[string]http.HandlerFunc{
		"GET /api/deploy/deployments/myapp/proposals": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"proposals": nil, "total": 0})
		},
	})
	var stdout, stderr bytes.Buffer
	if code := runAppsProposalsListCore(&stdout, &stderr, api.url(), "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no proposals") {
		t.Fatalf("empty state missing: %s", stdout.String())
	}
}
