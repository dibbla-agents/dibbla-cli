package apps

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProposalIDRe matches the opaque proposal ids issued by deploy-api. The CLI
// validates only their transport shape; proposal identity and visibility stay
// server-owned.
var ProposalIDRe = regexp.MustCompile(`^pr_[a-zA-Z0-9_-]{1,128}$`)

// MaintenanceEffectiveSettings is the server's effective org/app settings.
// In particular, app_version is the optimistic-concurrency token for app
// writes; there is deliberately no client-side settings state.
type MaintenanceEffectiveSettings struct {
	Enabled       bool   `json:"enabled"`
	OrgConfigured bool   `json:"org_configured,omitempty"`
	OrgEnabled    bool   `json:"org_enabled,omitempty"`
	AppConfigured bool   `json:"app_configured,omitempty"`
	AppEnabled    bool   `json:"app_enabled,omitempty"`
	Model         string `json:"model,omitempty"`
	NightlyCron   string `json:"nightly_cron,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	CooldownHours int    `json:"cooldown_hours,omitempty"`
	OrgVersion    int64  `json:"org_version,omitempty"`
	AppVersion    int64  `json:"app_version,omitempty"`
}

// MaintenanceRun is one execution from deploy-api's maintenance read model.
type MaintenanceRun struct {
	ExecutionID           string                   `json:"execution_id"`
	OrganizationID        string                   `json:"organization_id,omitempty"`
	DeploymentID          string                   `json:"deployment_id,omitempty"`
	DeploymentAlias       string                   `json:"deployment_alias,omitempty"`
	Trigger               string                   `json:"trigger"`
	Mode                  string                   `json:"mode"`
	Status                string                   `json:"status"`
	TerminalCode          string                   `json:"terminal_code,omitempty"`
	Outcome               string                   `json:"outcome,omitempty"`
	RunID                 string                   `json:"run_id,omitempty"`
	WorkflowRevision      string                   `json:"workflow_revision,omitempty"`
	ConfigRevision        string                   `json:"config_revision,omitempty"`
	ReservationID         string                   `json:"reservation_id,omitempty"`
	ToolAllowlist         []string                 `json:"tool_allowlist,omitempty"`
	Model                 string                   `json:"model,omitempty"`
	TokenAllowance        int64                    `json:"token_allowance,omitempty"`
	ToolCallLimit         int                      `json:"tool_call_limit,omitempty"`
	LeaseEpoch            int64                    `json:"lease_epoch,omitempty"`
	ActorUserID           string                   `json:"actor_user_id,omitempty"`
	CheckRunID            string                   `json:"check_run_id,omitempty"`
	UsedTokens            *int64                   `json:"used_tokens,omitempty"`
	ProposalSlotAttempted bool                     `json:"proposal_slot_attempted"`
	ProposalID            string                   `json:"proposal_id,omitempty"`
	Summary               string                   `json:"summary,omitempty"`
	Fingerprint           string                   `json:"fingerprint,omitempty"`
	EvidenceRefs          []string                 `json:"evidence_refs"`
	Finding               *MaintenanceFinding      `json:"finding,omitempty"`
	EvidenceGaps          []MaintenanceEvidenceGap `json:"evidence_gaps"`
	Deduplicated          bool                     `json:"deduplicated"`
	DeadlineAt            time.Time                `json:"deadline_at"`
	StartedAt             *time.Time               `json:"started_at,omitempty"`
	FinishedAt            *time.Time               `json:"finished_at,omitempty"`
	CreatedAt             time.Time                `json:"created_at"`
}

type MaintenanceFinding struct {
	Kind             string   `json:"kind"`
	Code             string   `json:"code"`
	Subject          string   `json:"subject"`
	CheckFingerprint string   `json:"check_fingerprint,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
}

type MaintenanceEvidenceGap struct {
	Tool   string `json:"tool"`
	Code   string `json:"code"`
	Cause  string `json:"cause"`
	Reason string `json:"reason"`
}

// IsTerminal reports the terminal vocabulary enforced by the server store.
func (r *MaintenanceRun) IsTerminal() bool {
	switch r.Status {
	case "completed", "error", "cancelled", "skipped_concurrent", "budget_exhausted":
		return true
	default:
		return false
	}
}

type MaintenanceStatus struct {
	Alias        string                       `json:"alias"`
	DeploymentID string                       `json:"deployment_id"`
	Settings     MaintenanceEffectiveSettings `json:"settings"`
	LastRun      *MaintenanceRun              `json:"last_run,omitempty"`
}

type MaintenanceSettingsAck struct {
	Alias        string `json:"alias"`
	DeploymentID string `json:"deployment_id"`
	Enabled      bool   `json:"enabled"`
}

// MaintenanceDispatch is the typed create/replay acknowledgement. Only
// dispatched and replayed promise a usable run id; execution_id is the public
// read-model handle used for polling.
type MaintenanceDispatch struct {
	Code          string `json:"code"`
	ExecutionID   string `json:"execution_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	ReservationID string `json:"reservation_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Replayed      bool   `json:"replayed,omitempty"`
}

type MaintenanceRunsResponse struct {
	Runs []MaintenanceRun `json:"runs"`
}

func GetMaintenanceStatus(apiURL, apiToken, alias string) (*MaintenanceStatus, []byte, error) {
	status, raw, err := doAppJSONRequest(http.MethodGet, maintenanceURL(apiURL, alias), apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out MaintenanceStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func SetMaintenanceEnabled(apiURL, apiToken, alias string, enabled bool, version int64) (*MaintenanceSettingsAck, []byte, error) {
	body := map[string]any{"enabled": enabled}
	if version > 0 {
		body["version"] = version
	}
	status, raw, err := doAppJSONRequest(http.MethodPut, maintenanceURL(apiURL, alias), apiToken, body, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out MaintenanceSettingsAck
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func StartMaintenanceRun(apiURL, apiToken, alias, mode, checkRunID, idempotencyKey string) (*MaintenanceDispatch, []byte, error) {
	body := map[string]any{"mode": mode}
	if checkRunID != "" {
		body["check_run_id"] = checkRunID
	}
	headers := map[string]string{"Idempotency-Key": idempotencyKey}
	status, raw, err := doAppJSONRequest(http.MethodPost, maintenanceURL(apiURL, alias)+"/runs", apiToken, body, headers)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out MaintenanceDispatch
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func GetMaintenanceRun(apiURL, apiToken, alias, executionID string) (*MaintenanceRun, []byte, error) {
	endpoint := maintenanceURL(apiURL, alias) + "/runs/" + url.PathEscape(executionID)
	status, raw, err := doAppJSONRequest(http.MethodGet, endpoint, apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out MaintenanceRun
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func ListMaintenanceRuns(apiURL, apiToken, alias string, limit int) (*MaintenanceRunsResponse, []byte, error) {
	endpoint := maintenanceURL(apiURL, alias) + "/runs"
	if limit > 0 {
		endpoint += "?limit=" + strconv.Itoa(limit)
	}
	status, raw, err := doAppJSONRequest(http.MethodGet, endpoint, apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out MaintenanceRunsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func maintenanceURL(apiURL, alias string) string {
	return fmt.Sprintf("%s/api/deploy/deployments/%s/maintenance-agent", strings.TrimSuffix(apiURL, "/"), url.PathEscape(alias))
}

// NewIdempotencyKey creates one key for one user intent. Callers that need to
// replay an intent supply --idempotency-key and reuse it; otherwise the CLI
// creates a fresh key and reports it in the dispatch document.
func NewIdempotencyKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return "run-" + hex.EncodeToString(b), nil
}

func doAppJSONRequest(method, endpoint, apiToken string, body any, headers map[string]string) (int, []byte, error) {
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
	for key, value := range headers {
		req.Header.Set(key, value)
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

func decodeAppResponse(err error, raw []byte) error {
	return fmt.Errorf("decode response: %w (body=%s)", err, string(raw))
}
