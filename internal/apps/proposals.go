package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Proposal struct {
	ID                     string                     `json:"id"`
	OrganizationID         string                     `json:"organization_id,omitempty"`
	DeploymentAlias        string                     `json:"deployment_alias"`
	AuthorID               string                     `json:"author_id"`
	AuthorName             string                     `json:"author_name,omitempty"`
	AuthorEmail            string                     `json:"author_email,omitempty"`
	Title                  string                     `json:"title"`
	Description            string                     `json:"description,omitempty"`
	Status                 string                     `json:"status"`
	GovernanceModel        string                     `json:"governance_model"`
	GovernanceVersion      int64                      `json:"governance_version"`
	BaseSHA                string                     `json:"base_sha"`
	HeadSHA                string                     `json:"head_sha"`
	SourceRef              string                     `json:"source_ref"`
	OptionsHash            string                     `json:"options_hash"`
	ChecksError            string                     `json:"checks_error,omitempty"`
	BuiltImages            map[string]string          `json:"built_images,omitempty"`
	PulledImages           map[string]string          `json:"pulled_images,omitempty"`
	DecisionBy             string                     `json:"decision_by,omitempty"`
	DecisionAt             *time.Time                 `json:"decision_at,omitempty"`
	CreatedAt              time.Time                  `json:"created_at"`
	UpdatedAt              time.Time                  `json:"updated_at"`
	Source                 string                     `json:"source"`
	MaintenanceExecutionID string                     `json:"maintenance_execution_id,omitempty"`
	Risk                   string                     `json:"risk,omitempty"`
	Evidence               map[string]json.RawMessage `json:"evidence,omitempty"`
}

type ProposalEvent struct {
	ID         int64                      `json:"id"`
	ProposalID string                     `json:"proposal_id"`
	Type       string                     `json:"type"`
	ActorID    string                     `json:"actor_id,omitempty"`
	Detail     map[string]json.RawMessage `json:"detail,omitempty"`
	CreatedAt  time.Time                  `json:"created_at"`
}

// ProposalDecision is server-owned capability data. The CLI renders it but
// never derives or persists approval eligibility itself.
type ProposalDecision struct {
	CanDecide        bool   `json:"can_decide"`
	EligibleReviewer bool   `json:"eligible_reviewer"`
	Reason           string `json:"reason"`
	Message          string `json:"message"`
	RequiredRole     string `json:"required_role"`
}

type ProposalReadModel struct {
	Proposal
	Events   []ProposalEvent  `json:"events"`
	Decision ProposalDecision `json:"decision"`
}

type ProposalsResponse struct {
	Proposals []Proposal `json:"proposals"`
	Total     int        `json:"total"`
}

type ProposalFileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status,omitempty"`
	OldPath   string `json:"old_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ProposalDiff struct {
	BaseSHA    string             `json:"base_sha"`
	HeadSHA    string             `json:"head_sha"`
	Files      []ProposalFileDiff `json:"files"`
	Additions  int                `json:"additions"`
	Deletions  int                `json:"deletions"`
	Truncated  bool               `json:"truncated"`
	TotalFiles int                `json:"total_files"`
}

type ProposalDiffResponse struct {
	Diff     ProposalDiff               `json:"diff"`
	Source   string                     `json:"source,omitempty"`
	Risk     string                     `json:"risk,omitempty"`
	Evidence map[string]json.RawMessage `json:"evidence,omitempty"`
}

func ListProposals(apiURL, apiToken, alias string) (*ProposalsResponse, []byte, error) {
	status, raw, err := doAppJSONRequest(http.MethodGet, proposalsURL(apiURL, alias), apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out ProposalsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func GetProposal(apiURL, apiToken, alias, proposalID string) (*ProposalReadModel, []byte, error) {
	status, raw, err := doAppJSONRequest(http.MethodGet, proposalURL(apiURL, alias, proposalID), apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out ProposalReadModel
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func GetProposalDiff(apiURL, apiToken, alias, proposalID string) (*ProposalDiffResponse, []byte, error) {
	status, raw, err := doAppJSONRequest(http.MethodGet, proposalURL(apiURL, alias, proposalID)+"/diff", apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out ProposalDiffResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func DecideProposal(apiURL, apiToken, alias, proposalID, action string) (*Proposal, []byte, error) {
	if action != "approve" && action != "deny" && action != "retry" {
		return nil, nil, fmt.Errorf("unsupported proposal action %q", action)
	}
	status, raw, err := doAppJSONRequest(http.MethodPost, proposalURL(apiURL, alias, proposalID)+"/"+action, apiToken, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusAccepted && status != http.StatusOK {
		return nil, raw, statusError(status, raw)
	}
	var out Proposal
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, decodeAppResponse(err, raw)
	}
	return &out, raw, nil
}

func proposalsURL(apiURL, alias string) string {
	return fmt.Sprintf("%s/api/deploy/deployments/%s/proposals", strings.TrimSuffix(apiURL, "/"), url.PathEscape(alias))
}

func proposalURL(apiURL, alias, proposalID string) string {
	return proposalsURL(apiURL, alias) + "/" + url.PathEscape(proposalID)
}
