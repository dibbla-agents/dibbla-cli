package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
)

// A secret entry request (DIB-1343) is how a secret's value reaches Dibbla
// without passing through this terminal: the request names the secret, the
// person opens its link in a signed-in browser and types the value there, and
// the CLI only ever learns whether that happened. Nothing in this file sends
// or receives a value — there is no field for one.

// Entry request states, as deploy-api reports them. Saving is the moment
// between the person pressing Save and the value being stored; it ends in
// completed, or back in pending if the store failed.
const (
	EntryStatePending   = "pending"
	EntryStateSaving    = "saving"
	EntryStateCompleted = "completed"
	EntryStateCancelled = "cancelled"
	EntryStateExpired   = "expired"
)

// EntryRequestInput is what `dibbla secrets request` and `dibbla env promote`
// ask for. Title and Explanation are shown to the person on the page, as text.
type EntryRequestInput struct {
	Name            string `json:"name"`
	DeploymentAlias string `json:"deployment_alias,omitempty"`
	ServiceName     string `json:"service_name,omitempty"`
	Title           string `json:"title,omitempty"`
	Explanation     string `json:"explanation,omitempty"`
	// PromoteEnv (DIB-1340) makes the request a promotion: when the person
	// enters a new value, the plain env var of the same name is removed and
	// the app restarted. Requires DeploymentAlias.
	PromoteEnv bool `json:"promote_env,omitempty"`
}

// EntryRequest is deploy-api's view of a request: the link, the state and the
// fixed target. It never carries a value.
type EntryRequest struct {
	RequestID        string     `json:"request_id"`
	State            string     `json:"state"`
	Name             string     `json:"name"`
	DeploymentAlias  string     `json:"deployment_alias,omitempty"`
	ServiceName      string     `json:"service_name,omitempty"`
	Title            string     `json:"title"`
	Explanation      string     `json:"explanation"`
	ReplacesExisting bool       `json:"replaces_existing"`
	PromotesEnv      bool       `json:"promotes_env,omitempty"`
	EntryURL         string     `json:"entry_url,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	CancelledAt      *time.Time `json:"cancelled_at,omitempty"`
}

// Terminal reports whether the request can no longer change.
func (r *EntryRequest) Terminal() bool {
	switch r.State {
	case EntryStateCompleted, EntryStateCancelled, EntryStateExpired:
		return true
	}
	return false
}

// StatusError is a non-2xx answer with the deploy-api envelope decoded, so a
// caller can tell ENV_FROM_MANIFEST from ROLE_FORBIDDEN without parsing prose.
type StatusError struct {
	Status  int
	Code    string
	Message string
	Body    []byte
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("API request failed with status %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ExitCode maps the refusal onto the CLI's shared ladder (3 auth, 4 not
// found, 5 request validation, 6 conflict, 7 timeout, 1 otherwise).
// deploy-api reports a bad request as 400; it is still the request's fault.
func (e *StatusError) ExitCode() int {
	if e.Status == http.StatusBadRequest {
		return 5
	}
	return apiclient.ExitCodeForStatus(e.Status)
}

func newStatusError(status int, body []byte) *StatusError {
	out := &StatusError{Status: status, Body: body}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		out.Code, out.Message = envelope.Error.Code, envelope.Error.Message
	}
	return out
}

// CreateEntryRequest is POST /secret-entry-requests. It answers the request as
// created plus the raw body, for --json.
func CreateEntryRequest(apiURL, apiToken string, in EntryRequestInput) (*EntryRequest, []byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}
	return doEntryRequest(http.MethodPost, makeAPIURL(apiURL, "/api/deploy/secret-entry-requests", nil), apiToken, raw, http.StatusCreated)
}

// GetEntryRequestStatus is GET /secret-entry-requests/{id}/status: the state
// of a request this person made. Somebody else's request is a 404.
func GetEntryRequestStatus(apiURL, apiToken, id string) (*EntryRequest, []byte, error) {
	endpoint := makeAPIURL(apiURL, "/api/deploy/secret-entry-requests/"+url.PathEscape(id)+"/status", nil)
	return doEntryRequest(http.MethodGet, endpoint, apiToken, nil, http.StatusOK)
}

func doEntryRequest(method, endpoint, apiToken string, body []byte, want int) (*EntryRequest, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: requestTimeout}).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != want {
		return nil, raw, newStatusError(resp.StatusCode, raw)
	}
	var out EntryRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, fmt.Errorf("failed to parse response: %w", err)
	}
	return &out, raw, nil
}
