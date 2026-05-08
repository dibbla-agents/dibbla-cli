// Package render renders dibbla deploy progress to the terminal. The
// server streams NDJSON DeployEvent values and the CLI picks one of four
// renderers (TTY, log, quiet, JSON) based on flags and stdout type. The
// design language is documented in the design handoff bundle (Anthropic
// Design — "Dibbla TUI" project, cli-output.jsx, deploy variants 4a–4d).
package render

import (
	"fmt"
	"io"
	"time"
)

// DeployEvent mirrors the server's discriminated-union event line. Field
// names and types must stay in sync with
// app-hosting-service/deploy-api/internal/handler/deployments/events.go —
// when a field is added there it should be added here too.
type DeployEvent struct {
	Type string    `json:"type"`
	Ts   time.Time `json:"ts"`

	Step      string `json:"step,omitempty"`
	StepIndex int    `json:"step_index,omitempty"`
	StepCount int    `json:"step_count,omitempty"`
	State     string `json:"state,omitempty"`
	Name      string `json:"name,omitempty"`
	Cached    bool   `json:"cached,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms,omitempty"`
	Source    string `json:"source,omitempty"`

	Log string `json:"log,omitempty"`

	Result *DeployResult `json:"result,omitempty"`
	Error  *DeployError  `json:"error,omitempty"`
}

// DeployResult is the success payload. We keep only the fields the
// renderers actually use — extra fields the server may add are ignored on
// decode (json.Unmarshal default).
type DeployResult struct {
	Status     string         `json:"status"`
	Deployment ResultDeployment `json:"deployment"`
	VCSCommit  string         `json:"vcs_commit,omitempty"`
}

type ResultDeployment struct {
	ID       string        `json:"id"`
	Alias    string        `json:"alias"`
	URL      string        `json:"url"`
	Status   string        `json:"status"`
	Services []ServiceView `json:"services,omitempty"`
}

// ServiceView is the per-service entry the renderers display. Mirrors the
// server-side models.DeploymentServiceView.
type ServiceView struct {
	Name          string       `json:"name"`
	Image         string       `json:"image,omitempty"`
	Port          *int         `json:"port,omitempty"`
	Replicas      int          `json:"replicas"`
	ReadyReplicas int          `json:"ready_replicas"`
	IsPublic      bool         `json:"is_public"`
	IsBuilt       bool         `json:"is_built"`
	Status        string       `json:"status,omitempty"`
	Stateful      bool         `json:"stateful,omitempty"`
	Routes        []RouteView  `json:"routes,omitempty"`
	Volumes       []VolumeView `json:"volumes,omitempty"`
}

// VolumeView mirrors models.ServiceVolumeView. Surviving across GETs/LISTs
// requires the deploy-api to read the per-workload service-spec annotation;
// legacy deployments without the annotation will have this nil.
type VolumeView struct {
	Path   string `json:"path"`
	Size   string `json:"size"`
	Access string `json:"access,omitempty"`
}

// RouteView is one externally-routable endpoint surfaced from the server.
// Hostname is the resolved FQDN; the renderer prints copy-pasteable
// connection info from this directly.
type RouteView struct {
	Type string `json:"type"`
	// Port is the backend container port from the manifest (e.g. 27017 for
	// Mongo). Not the port a client should dial — that's ExternalPort.
	Port         int    `json:"port"`
	TLS          string `json:"tls,omitempty"`
	Hostname     string `json:"hostname"`
	ExternalPort int    `json:"external_port,omitempty"`
}

// ConnectionURL returns a copy-pasteable connection string for one route.
// Engine-agnostic: we infer a scheme from type+tls and let the user fill in
// credentials. Returns "" for the http/https types since those already show
// up as the deployment's primary URL.
func (r RouteView) ConnectionURL() string {
	if r.Type != "tcp" {
		return ""
	}
	// type=tcp + tls: edge|passthrough → external connection through
	// tcpproxy's SNI listener. ExternalPort is the public port (typically
	// 443); fall back to the manifest port when the server didn't supply
	// one (older deploy-api).
	port := r.ExternalPort
	if port == 0 {
		port = r.Port
	}
	return fmt.Sprintf("tcp+tls://%s:%d", r.Hostname, port)
}

// DeployError is the failure payload. Mirrors the server's
// DeployErrorEvent.
type DeployError struct {
	APIError    *APIError            `json:"api_error"`
	StatusCode  int                  `json:"status_code"`
	FailedStep  string               `json:"failed_step,omitempty"`
	StepIndex   int                  `json:"step_index,omitempty"`
	StepCount   int                  `json:"step_count,omitempty"`
	BuildLogs   string               `json:"build_logs,omitempty"`
	ParsedItems []ParsedBuildError   `json:"parsed_errors,omitempty"`
	RetryCmd    string               `json:"retry_cmd,omitempty"`
}

type APIError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	RequestID    string `json:"request_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Logs         string `json:"logs,omitempty"`
}

type ParsedBuildError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
}

// Renderer consumes a stream of DeployEvent values from the server and
// terminates with OnDone, returning the process exit code the CLI should
// use. Renderers are stateful — one instance per deploy invocation.
type Renderer interface {
	OnEvent(ev DeployEvent)
	OnDone() int
}

// formatElapsed renders a duration with the same shape the design uses
// (e.g. "0.4s", "14.7s", "1m 28s"). Compact and stable in the right-
// aligned elapsed column.
func formatElapsed(ms int64) string {
	if ms <= 0 {
		return ""
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %ds", mins, secs)
}

// printlnTo is a small helper that ignores Write errors — matches the
// pattern Go CLIs typically use for terminal output (errors writing to
// stdout are non-recoverable from the CLI's perspective).
func printlnTo(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}
