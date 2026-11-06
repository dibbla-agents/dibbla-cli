package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type FindingsOptions struct {
	DeploymentID, MinSeverity, Kind, Service, Cursor string
	FixableOnly                                      bool
	FixableOnlySet                                   bool
	Limit                                            int
}

func GetFindings(apiURL, apiToken, alias string, opts FindingsOptions) (*SecurityFindingsView, []byte, error) {
	q := url.Values{}
	for key, value := range map[string]string{"deployment_id": opts.DeploymentID, "min_severity": opts.MinSeverity, "kind": opts.Kind, "service": opts.Service, "cursor": opts.Cursor} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if opts.Limit != 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.FixableOnly || opts.FixableOnlySet {
		q.Set("fixable_only", strconv.FormatBool(opts.FixableOnly))
	}
	status, raw, err := doChecksRequest(http.MethodGet, strings.TrimSuffix(apiURL, "/")+"/api/deploy/deployments/"+url.PathEscape(alias)+"/security-scan/findings?"+q.Encode(), apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, nil, statusError(status, raw)
	}
	var out SecurityFindingsView
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode Security scan: %w", err)
	}
	if out.Status == "" || out.Findings == nil || out.HasMore != (out.NextCursor != "") {
		return nil, nil, fmt.Errorf("incomplete Security scan response")
	}
	return &out, raw, nil
}

// AllFindings buffers until every page has been read. A changed scan fails
// without emitting a misleading partial JSON document.
func AllFindings(apiURL, apiToken, alias string, opts FindingsOptions) (*SecurityFindingsView, []byte, error) {
	if opts.Cursor != "" {
		return nil, nil, fmt.Errorf("--all cannot be combined with --cursor")
	}
	out, _, err := GetFindings(apiURL, apiToken, alias, opts)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for out.HasMore {
		if seen[out.NextCursor] {
			return nil, nil, fmt.Errorf("Security scan repeated a cursor; discard pages and retry")
		}
		seen[out.NextCursor] = true
		opts.Cursor = out.NextCursor
		page, _, err := GetFindings(apiURL, apiToken, alias, opts)
		if err != nil {
			return nil, nil, err
		}
		if page.ScanID != out.ScanID || page.DeploymentID != out.DeploymentID || page.Total != out.Total || page.Offset != len(out.Findings) || len(page.Findings) == 0 {
			return nil, nil, fmt.Errorf("Security scan pages disagree; discard pages and retry")
		}
		out.Findings = append(out.Findings, page.Findings...)
		out.HasMore, out.NextCursor = page.HasMore, page.NextCursor
		out.Running, out.RunningDeploymentID = page.Running, page.RunningDeploymentID
	}
	if len(out.Findings) != out.Total {
		return nil, nil, fmt.Errorf("Security scan finding count does not match total")
	}
	raw, err := json.Marshal(out)
	return out, raw, err
}
