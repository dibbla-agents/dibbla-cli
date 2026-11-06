package apps

import (
	"encoding/json"
	"time"
)

// Security scan wire contract; mirrored from the findings endpoint.
type AgentFinding struct {
	// Kind is "vulnerability" or "secret".
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// ID is the advisory id (CVE-…, GHSA-…) or the gitleaks rule.
	ID string `json:"id"`
	// Service and Image name the image the vulnerability was found in;
	// empty for a secret, which is found in the source tree.
	Service string `json:"service,omitempty"`
	Image   string `json:"image,omitempty"`
	// Package, Version and Type are the installed artifact the advisory
	// matched. Type is syft's vocabulary (apk, deb, npm, go-module, …) and
	// is what tells an application dependency from an operating-system one.
	Package string `json:"package,omitempty"`
	Version string `json:"version,omitempty"`
	Type    string `json:"type,omitempty"`
	// FixedIn is the first version that closes the advisory; empty means
	// there is no fix to propose yet. FixState is grype's own word for why.
	FixedIn  string `json:"fixed_in,omitempty"`
	FixState string `json:"fix_state,omitempty"`
	// Advisory is the URL the match came from.
	Advisory string `json:"advisory,omitempty"`
	// File, Line, Description and Fingerprint describe a secret. The matched
	// secret itself is never carried — see SecretFinding.
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Description string `json:"description,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Key is the same key Findings uses, so a finding here can be matched
	// against the brief's new-finding list without re-deriving it.
	Key string `json:"key"`
}

type SecurityFindingsView struct {
	Alias               string          `json:"alias"`
	ScanID              string          `json:"scan_id"`
	DeploymentID        string          `json:"deployment_id"`
	RunningDeploymentID string          `json:"running_deployment_id"`
	CommitSHA           string          `json:"commit_sha,omitempty"`
	Status              string          `json:"status"`
	StartedAt           *time.Time      `json:"started_at"`
	CompletedAt         *time.Time      `json:"completed_at"`
	RematchedAt         *time.Time      `json:"rematched_at"`
	Running             bool            `json:"running"`
	Scanners            json.RawMessage `json:"scanners"`
	Errors              []string        `json:"errors"`
	Summary             json.RawMessage `json:"summary"`
	Secrets             int             `json:"secrets"`
	Filter              json.RawMessage `json:"filter"`
	Total               int             `json:"total"`
	Offset              int             `json:"offset"`
	Limit               int             `json:"limit"`
	HasMore             bool            `json:"has_more"`
	NextCursor          string          `json:"next_cursor,omitempty"`
	Findings            []AgentFinding  `json:"findings"`
}
