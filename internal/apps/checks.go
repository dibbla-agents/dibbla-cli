package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// AliasRe is the alias pattern deploy-api enforces on every write
// (internal/handler/deployments/deploy.go aliasPattern). Mirrored client-side
// so obviously invalid aliases are rejected before any request is issued.
var AliasRe = regexp.MustCompile(`^[a-z][a-z0-9-]{2,62}[a-z0-9]$`)

// CheckIDRe is the check-id pattern from the application-checks source schema
// (dibbla-checks.yaml: ^[a-z][a-z0-9-]*$, max 63 chars). Mirrored client-side
// for the same reason as AliasRe.
var CheckIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// StatusError is a non-2xx API answer. It carries the HTTP status plus the
// server's stable `code` and prose `message` from the error envelope, so
// callers can print "code + detail" and map the status onto the CLI's exit
// code ladder without scraping stderr.
type StatusError struct {
	Status  int
	Code    string
	Message string
	Body    string
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("API error %d (%s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("API error %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// ExitCode maps the transport failure onto the shared CLI ladder:
// 3 auth/permission, 4 not found, 5 request validation, 6 conflict, 7
// timeout, 1 anything else. deploy-api reports request validation as 400
// where the slim workflow API uses 422; both mean "the request was wrong",
// so both map to 5. Everything else defers to apiclient so this stays one
// ladder rather than a per-command one.
func (e *StatusError) ExitCode() int {
	if e.Status == http.StatusBadRequest {
		return 5
	}
	return apiclient.ExitCodeForStatus(e.Status)
}

// CheckDefinition is one check from GET /deployments/{alias}/application-checks.
type CheckDefinition struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Schedule         string   `json:"schedule"`
	FailureThreshold int      `json:"failure_threshold"`
	Cooldown         string   `json:"cooldown"`
	RunDeadline      string   `json:"run_deadline"`
	Classification   string   `json:"classification"`
	Dependencies     []string `json:"dependencies,omitempty"`
	Enabled          bool     `json:"enabled"`
	SchemaVersion    int      `json:"schema_version"`
	ConfigRevision   string   `json:"config_revision"`
}

// CheckSettings is the org- or app-scope enablement record.
type CheckSettings struct {
	OrganizationID     string  `json:"organization_id"`
	ScopeType          string  `json:"scope_type"`
	ScopeID            string  `json:"scope_id"`
	Enabled            bool    `json:"enabled"`
	CadenceOverride    *string `json:"cadence_override,omitempty"`
	Cooldown           *string `json:"cooldown,omitempty"`
	NightlyTokenBudget *int64  `json:"nightly_token_budget,omitempty"`
	FailureThreshold   *int    `json:"failure_threshold,omitempty"`
	Version            int64   `json:"version"`
}

// ChecksResponse is GET /deployments/{alias}/application-checks: the app's
// definitions plus the current aggregate state. An app whose org has the
// capability enabled but no dibbla-checks.yaml gets configured=false and an
// empty definitions list.
type ChecksResponse struct {
	SchemaVersion            int               `json:"schema_version"`
	DeploymentID             string            `json:"deployment_id,omitempty"`
	DeploymentAlias          string            `json:"deployment_alias"`
	Configured               bool              `json:"configured"`
	ConfigRevision           string            `json:"config_revision,omitempty"`
	TargetDeploymentRevision string            `json:"target_deployment_revision,omitempty"`
	ConfigurationErrorCode   string            `json:"configuration_error_code,omitempty"`
	ConfigurationErrorDetail string            `json:"configuration_error_detail,omitempty"`
	Definitions              []CheckDefinition `json:"definitions"`
	Settings                 *CheckSettings    `json:"settings,omitempty"`
}

// CheckExecution is the parent execution returned by POST
// /deployments/{alias}/application-checks/run (wrapped as {"execution": …})
// and by GET /application-check-executions/{executionId}. Status is queued,
// running, or one of the terminals pass/fail/error/indeterminate/canceled;
// skipped_concurrent is returned instead of a started execution when another
// run already holds the app lease.
type CheckExecution struct {
	ID                string     `json:"execution_id"`
	OrganizationID    string     `json:"organization_id"`
	DeploymentID      string     `json:"deployment_id"`
	Trigger           string     `json:"trigger"`
	Status            string     `json:"status"`
	TerminalCode      string     `json:"terminal_code,omitempty"`
	RunID             string     `json:"run_id"`
	WorkflowRevision  string     `json:"workflow_revision"`
	ConfigRevision    string     `json:"config_revision"`
	RequestedCheckIDs []string   `json:"requested_check_ids"`
	ReservationID     string     `json:"reservation_id"`
	ToolAllowlist     []string   `json:"tool_allowlist"`
	LeaseEpoch        int64      `json:"lease_epoch"`
	ActorUserID       string     `json:"actor_user_id,omitempty"`
	DeadlineAt        time.Time  `json:"deadline_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

// IsTerminal reports whether the execution status is one clients stop polling
// on. Every terminal implies a product exit code (see
// exitCodeForExecution in internal/cmd/deploy).
func (e *CheckExecution) IsTerminal() bool {
	switch e.Status {
	case "pass", "fail", "error", "indeterminate", "canceled", "skipped_concurrent":
		return true
	}
	return false
}

// CheckRun is one typed child result, as returned by the per-check history
// endpoint GET /deployments/{alias}/application-checks/{checkId}/runs. The
// field set mirrors the proposal's typed result document.
type CheckRun struct {
	SchemaVersion            int            `json:"schema_version"`
	RunID                    string         `json:"run_id"`
	WorkflowRunID            string         `json:"workflow_run_id"`
	CheckID                  string         `json:"check_id"`
	DeploymentID             string         `json:"deployment_id"`
	DeploymentAlias          string         `json:"deployment_alias"`
	ConfigRevision           string         `json:"config_revision"`
	TargetDeploymentRevision string         `json:"target_deployment_revision"`
	TargetImageDigests       []string       `json:"target_image_digests"`
	Trigger                  string         `json:"trigger"`
	Outcome                  string         `json:"outcome"`
	Code                     string         `json:"code"`
	Summary                  string         `json:"summary"`
	StartedAt                time.Time      `json:"started_at"`
	FinishedAt               time.Time      `json:"finished_at"`
	DurationMs               int64          `json:"duration_ms"`
	Tokens                   CheckRunTokens `json:"tokens"`
	ToolCalls                int            `json:"tool_calls"`
	TransitionID             string         `json:"transition_id"`
	Fingerprint              string         `json:"fingerprint"`
}

// CheckRunTokens is the token breakdown of one run.
type CheckRunTokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Total      int64 `json:"total"`
}

// CheckRunsPage is one page of per-check history. NextCursor is the opaque
// stable cursor of this child-history snapshot; empty when this page is the
// last one.
type CheckRunsPage struct {
	Runs       []CheckRun `json:"runs"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// GetApp fetches one deployment by alias (GET /api/deploy/deployments/{alias})
// — the endpoint `dibbla apps get` renders. The raw body is returned alongside
// the parsed deployment so --json can emit the server document verbatim.
func GetApp(apiURL, apiToken, alias string) (*Deployment, []byte, error) {
	status, body, err := doChecksRequest(http.MethodGet,
		fmt.Sprintf("%s/api/deploy/deployments/%s", strings.TrimSuffix(apiURL, "/"), alias),
		apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var dep Deployment
	if err := json.Unmarshal(body, &dep); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &dep, body, nil
}

// ListChecks returns the app's check definitions and aggregate state.
func ListChecks(apiURL, apiToken, alias string) (*ChecksResponse, []byte, error) {
	status, body, err := doChecksRequest(http.MethodGet, checksURL(apiURL, alias), apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var out ChecksResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &out, body, nil
}

// RunChecks creates one execution for all or the selected checks (POST
// …/application-checks/run) and returns the parent execution the server
// answered with, plus the raw body for --json.
func RunChecks(apiURL, apiToken, alias string, checkIDs []string) (*CheckExecution, []byte, error) {
	reqBody := map[string]any{}
	if len(checkIDs) > 0 {
		reqBody["check_ids"] = checkIDs
	}
	status, body, err := doChecksRequest(http.MethodPost, checksURL(apiURL, alias)+"/run", apiToken, reqBody)
	if err != nil {
		return nil, nil, err
	}
	// 202 Accepted carries {"execution": …}; accept 200 the same way.
	if status != http.StatusAccepted && status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var wrapped struct {
		Execution CheckExecution `json:"execution"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &wrapped.Execution, body, nil
}

// GetExecution fetches one parent execution by id.
func GetExecution(apiURL, apiToken, alias, executionID string) (*CheckExecution, error) {
	status, body, err := doChecksRequest(http.MethodGet,
		fmt.Sprintf("%s/api/deploy/deployments/%s/application-check-executions/%s",
			strings.TrimSuffix(apiURL, "/"), alias, executionID),
		apiToken, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError(status, body)
	}
	var execution CheckExecution
	if err := json.Unmarshal(body, &execution); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &execution, nil
}

// GetCheckRuns fetches one page of a single check's history. limit <= 0 sends
// no limit and lets the server choose.
func GetCheckRuns(apiURL, apiToken, alias, checkID string, limit int) (*CheckRunsPage, []byte, error) {
	endpoint := fmt.Sprintf("%s/api/deploy/deployments/%s/application-checks/%s/runs",
		strings.TrimSuffix(apiURL, "/"), alias, checkID)
	if limit > 0 {
		endpoint += "?limit=" + strconv.Itoa(limit)
	}
	status, body, err := doChecksRequest(http.MethodGet, endpoint, apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var page CheckRunsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &page, body, nil
}

// SetChecksEnabled enables or disables the app's checks runtime via PUT
// …/application-check-settings with {"enabled": …}. The raw body is returned
// for callers that need the server document verbatim.
func SetChecksEnabled(apiURL, apiToken, alias string, enabled bool) (*CheckSettings, []byte, error) {
	status, body, err := doChecksRequest(http.MethodPut,
		fmt.Sprintf("%s/api/deploy/deployments/%s/application-check-settings",
			strings.TrimSuffix(apiURL, "/"), alias),
		apiToken, map[string]any{"enabled": enabled})
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var settings CheckSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &settings, body, nil
}

func checksURL(apiURL, alias string) string {
	return fmt.Sprintf("%s/api/deploy/deployments/%s/application-checks",
		strings.TrimSuffix(apiURL, "/"), alias)
}

// doChecksRequest performs one JSON request against the deploy API. The
// Authorization header is set here; the X-Org-ID header is attached by the
// orgctx transport wrapping http.DefaultClient's transport, exactly like every
// other internal/* API client.
func doChecksRequest(method, endpoint, apiToken string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// statusError converts a non-2xx body into a StatusError, decoding the
// deploy-api error envelope {"status":"error","error":{"code","message"}} so
// the stable code and prose detail survive.
func statusError(status int, body []byte) *StatusError {
	out := &StatusError{Status: status, Body: string(body)}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		out.Code = envelope.Error.Code
		out.Message = envelope.Error.Message
	}
	return out
}
