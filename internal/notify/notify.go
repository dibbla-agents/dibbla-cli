// Package notify is the client for Dibbla's notification service, reached
// through the API gateway at /api/notify (DIB-1102): the subscriptions that
// decide where alerts go, the event catalog they pick from, a real test send,
// and the history of what happened and whether it arrived.
package notify

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

// Subscription is one row as the builder surface shows it.
type Subscription struct {
	ID          string    `json:"id"`
	Scope       string    `json:"scope"` // personal | organization
	EventType   string    `json:"event_type"`
	Label       string    `json:"label,omitempty"`
	App         string    `json:"app,omitempty"`
	Channel     string    `json:"channel"`
	Target      string    `json:"target,omitempty"`
	MinSeverity string    `json:"min_severity"`
	Digest      string    `json:"digest"`
	Enabled     bool      `json:"enabled"`
	Origin      string    `json:"origin,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type SubscriptionList struct {
	Subscriptions         []Subscription `json:"subscriptions"`
	CanManageOrganization bool           `json:"can_manage_organization"`
}

// AddRequest is POST /api/me/subscriptions.
type AddRequest struct {
	EventType    string `json:"event_type"`
	App          string `json:"app,omitempty"`
	MinSeverity  string `json:"min_severity,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Target       string `json:"target,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Organization bool   `json:"organization,omitempty"`
}

type AddResult struct {
	Subscription Subscription `json:"subscription"`
	Created      bool         `json:"created"`
}

// Delivery is one delivery's outcome.
type Delivery struct {
	ID             string     `json:"id"`
	SubscriptionID string     `json:"subscription_id"`
	Scope          string     `json:"scope,omitempty"`
	Channel        string     `json:"channel"`
	Target         string     `json:"target,omitempty"`
	Status         string     `json:"status"` // sent | pending | failed | dropped
	Attempts       int        `json:"attempts"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	Reason         string     `json:"reason,omitempty"`
}

type TestResult struct {
	EventID      string       `json:"event_id"`
	Subscription Subscription `json:"subscription"`
	Delivery     Delivery     `json:"delivery"`
}

type Event struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Headline   string     `json:"headline"`
	Subject    string     `json:"subject,omitempty"`
	App        string     `json:"app,omitempty"`
	Severity   string     `json:"severity"`
	CreatedAt  time.Time  `json:"created_at"`
	Deliveries []Delivery `json:"deliveries"`
}

type History struct {
	Events []Event `json:"events"`
	Scope  string  `json:"scope"`
}

type CatalogEvent struct {
	EventType   string `json:"event_type"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity,omitempty"`
}

type CatalogGroup struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Family      CatalogEvent   `json:"family"`
	Events      []CatalogEvent `json:"events"`
}

type Catalog struct {
	Groups []CatalogGroup `json:"groups"`
}

// Error is a non-2xx answer. notify-service answers {"error":"<sentence>"},
// and the sentence is written for the person who asked, so it is shown as is.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("the notification service answered %d", e.Status)
}

// ExitCode is the CLI's shared ladder: 3 auth, 4 not found, 5 validation,
// 6 conflict, 1 otherwise.
func (e *Error) ExitCode() int {
	if e.Status == http.StatusBadRequest {
		return 5
	}
	return apiclient.ExitCodeForStatus(e.Status)
}

func endpoint(apiURL, path string) string {
	return strings.TrimSuffix(apiURL, "/") + "/api/notify" + path
}

func do(method, apiURL, token, path string, body, out any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, endpoint(apiURL, path), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A test send waits for the channel's answer; give it room.
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return raw, &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("unreadable answer: %w", err)
		}
	}
	return raw, nil
}

func appQuery(app string, extra url.Values) string {
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	if app != "" {
		q.Set("app", app)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func List(apiURL, token, app string) (*SubscriptionList, []byte, error) {
	var out SubscriptionList
	raw, err := do(http.MethodGet, apiURL, token, "/me/subscriptions"+appQuery(app, nil), nil, &out)
	return &out, raw, err
}

func Add(apiURL, token string, req AddRequest) (*AddResult, []byte, error) {
	var out AddResult
	raw, err := do(http.MethodPost, apiURL, token, "/me/subscriptions", req, &out)
	return &out, raw, err
}

func Remove(apiURL, token, id string) ([]byte, error) {
	return do(http.MethodDelete, apiURL, token, "/me/subscriptions/"+url.PathEscape(id), nil, nil)
}

func Test(apiURL, token, id string) (*TestResult, []byte, error) {
	var out TestResult
	raw, err := do(http.MethodPost, apiURL, token, "/me/subscriptions/"+url.PathEscape(id)+"/test", nil, &out)
	return &out, raw, err
}

func GetHistory(apiURL, token, app string, limit int) (*History, []byte, error) {
	var out History
	extra := url.Values{}
	if limit > 0 {
		extra.Set("limit", fmt.Sprint(limit))
	}
	raw, err := do(http.MethodGet, apiURL, token, "/me/notification-history"+appQuery(app, extra), nil, &out)
	return &out, raw, err
}

func GetCatalog(apiURL, token string) (*Catalog, []byte, error) {
	var out Catalog
	raw, err := do(http.MethodGet, apiURL, token, "/event-catalog", nil, &out)
	return &out, raw, err
}
