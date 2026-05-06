// Package applogs talks to deploy-api's per-app /logs endpoint, which streams
// NDJSON entries (one log line per row) to the caller.
package applogs

import (
	"context"
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

// Entry is a single log line as the server emits it. Mirrors
// deploy-api/internal/loki.Entry.
type Entry struct {
	Timestamp time.Time         `json:"ts"`
	Line      string            `json:"line"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Options controls the GET /deployments/{alias}/logs query string.
type Options struct {
	Since   time.Duration // 0 = server default (15m)
	Limit   int           // 0 = server default
	Tail    int           // 0 = none; >0 = last-N lines mode (kubectl --tail)
	Grep    string        // optional regex line filter
	Follow  bool
	Service string // optional per-service filter (forwarded as ?service=)
}

// Stream opens the streaming connection to the logs endpoint and returns the
// raw response body. The caller is responsible for closing it. Each line in
// the body is one JSON object — either an Entry or a `{"error":"..."}`
// envelope appended by the server when it encountered a failure mid-stream.
func Stream(ctx context.Context, apiURL, apiToken, alias string, opts Options) (io.ReadCloser, error) {
	apiURL = strings.TrimSuffix(apiURL, "/")
	u, err := url.Parse(fmt.Sprintf("%s/api/deploy/deployments/%s/logs", apiURL, alias))
	if err != nil {
		return nil, fmt.Errorf("invalid api url: %w", err)
	}

	q := u.Query()
	if opts.Since > 0 {
		q.Set("since", opts.Since.String())
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Tail > 0 {
		q.Set("tail", strconv.Itoa(opts.Tail))
	}
	if opts.Grep != "" {
		q.Set("grep", opts.Grep)
	}
	if opts.Follow {
		q.Set("follow", "true")
	}
	if opts.Service != "" {
		q.Set("service", opts.Service)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/x-ndjson")

	// Long timeout for follow; for range it's bounded by the server's own work.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logs request: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return resp.Body, nil
}

// PodStreamOptions controls GET /deployments/{alias}/services/{service}/logs.
// This endpoint streams text/plain (not NDJSON) — each line is prefixed with
// the pod name as `[<pod>] <line>` so the caller can tell which replica
// produced each row.
type PodStreamOptions struct {
	Tail   int  // 0 = no tail param; >0 = last-N lines per pod
	Follow bool
}

// StreamPodService opens a pod-log stream for one service. Returns the raw
// response body (text/plain). The caller closes it. Validates the service
// name client-side so an obvious typo fails before the HTTP roundtrip.
func StreamPodService(ctx context.Context, apiURL, apiToken, alias, service string, opts PodStreamOptions) (io.ReadCloser, error) {
	if !servicePodNameRe.MatchString(service) {
		return nil, fmt.Errorf("service name %q does not match %s", service, servicePodNameRe.String())
	}
	apiURL = strings.TrimSuffix(apiURL, "/")
	u, err := url.Parse(fmt.Sprintf("%s/api/deploy/deployments/%s/services/%s/logs", apiURL, alias, service))
	if err != nil {
		return nil, fmt.Errorf("invalid api url: %w", err)
	}
	q := u.Query()
	if opts.Follow {
		q.Set("follow", "true")
	}
	if opts.Tail > 0 {
		q.Set("tail", strconv.Itoa(opts.Tail))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "text/plain")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logs request: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return resp.Body, nil
}

// servicePodNameRe mirrors the server's restartServiceNameRe.
var servicePodNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// HTTPError wraps a non-2xx response from the logs endpoint.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("logs API %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("logs API %d", e.Status)
}

// DecodeLine parses a single NDJSON row into either an Entry or an error
// envelope. When the row is an error envelope, returns ok=false and the
// embedded message via err.
func DecodeLine(line []byte) (Entry, bool, error) {
	// Probe for an error envelope first — these are short and only appear
	// when the server hits a fault mid-stream.
	var probe struct {
		Err string `json:"error"`
	}
	if json.Unmarshal(line, &probe) == nil && probe.Err != "" {
		return Entry{}, false, fmt.Errorf("server: %s", probe.Err)
	}

	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		return Entry{}, false, fmt.Errorf("decode logs row: %w", err)
	}
	return e, true, nil
}
