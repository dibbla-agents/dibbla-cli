package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A deployment operation is the durable deploy behind a `git push` to main
// (DIB-903) — the same ledger an agent-started deploy runs in. The CLI reads
// it through GET /deployment-operations/{id}[/events|/logs], the org-scoped
// human twins of the platform actor's routes.

// Operation is the status document.
type Operation struct {
	OperationID string          `json:"operation_id"`
	Kind        string          `json:"kind"`
	Alias       string          `json:"alias"`
	Phase       string          `json:"phase"`
	Terminal    bool            `json:"terminal"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Failure     *struct {
		Code    string `json:"code"`
		Summary string `json:"summary"`
	} `json:"failure,omitempty"`
	Source *struct {
		Repo      string `json:"repo"`
		Ref       string `json:"ref"`
		CommitSHA string `json:"commit_sha"`
	} `json:"source,omitempty"`
}

// OperationResult is the success payload of a deploy operation.
type OperationResult struct {
	Result       string `json:"result"`
	Alias        string `json:"alias"`
	URL          string `json:"url"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	CommitSHA    string `json:"commit_sha"`
}

// OperationEvent is one lifecycle line of an operation.
type OperationEvent struct {
	Sequence  int64     `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Level     string    `json:"level"`
	Text      string    `json:"text"`
}

// OperationLogLine is one build-log line of an operation.
type OperationLogLine struct {
	Sequence  int64     `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Line      string    `json:"line"`
}

func operationURL(apiURL, id, suffix string) string {
	return fmt.Sprintf("%s/api/deploy/deployment-operations/%s%s", strings.TrimSuffix(apiURL, "/"), url.PathEscape(id), suffix)
}

// GetOperation reads an operation's status. The raw body is returned so
// --json can emit the server document verbatim.
func GetOperation(apiURL, apiToken, id string) (*Operation, []byte, error) {
	status, body, err := doChecksRequest(http.MethodGet, operationURL(apiURL, id, ""), apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var op Operation
	if err := json.Unmarshal(body, &op); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &op, body, nil
}

// OperationEvents pages the operation's events after a cursor.
func OperationEvents(apiURL, apiToken, id string, after int64) ([]OperationEvent, bool, error) {
	var page struct {
		Events  []OperationEvent `json:"events"`
		HasMore bool             `json:"has_more"`
	}
	if err := getPage(operationURL(apiURL, id, fmt.Sprintf("/events?after=%d&limit=200", after)), apiToken, &page); err != nil {
		return nil, false, err
	}
	return page.Events, page.HasMore, nil
}

// OperationLogs pages the operation's build log after a cursor.
func OperationLogs(apiURL, apiToken, id string, after int64) ([]OperationLogLine, bool, error) {
	var page struct {
		Lines   []OperationLogLine `json:"lines"`
		HasMore bool               `json:"has_more"`
	}
	if err := getPage(operationURL(apiURL, id, fmt.Sprintf("/logs?after=%d&limit=200", after)), apiToken, &page); err != nil {
		return nil, false, err
	}
	return page.Lines, page.HasMore, nil
}

func getPage(endpoint, apiToken string, out any) error {
	status, body, err := doChecksRequest(http.MethodGet, endpoint, apiToken, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return statusError(status, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return nil
}
