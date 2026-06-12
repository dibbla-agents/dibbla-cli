package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Log renders the non-TTY (CI / piped) variant: ISO-8601 timestamped log
// lines with `[level] scope    msg key=val key=val` shape, no spinners,
// no cursor movement, and on failure a single structured JSON event on
// stderr that coding agents can parse without reading the human-readable
// lines. The format matches the design's `deploy · CI` and
// `deploy · CI failure` mocks (cli-output.jsx:418-512).
type Log struct {
	out, err io.Writer

	stepCount int
	result    *DeployResult
	errEv     *DeployError
	startedAt time.Time
}

func NewLog(out, err io.Writer) *Log {
	return &Log{out: out, err: err, startedAt: time.Now()}
}

func (l *Log) OnEvent(ev DeployEvent) {
	switch ev.Type {
	case "deploy":
		l.line("info", "deploy", ev.Source)
	case "build":
		l.handleBuild(ev)
	case "rollout":
		l.line("info", "rollout", "phase="+ev.State+joinFields(ev.Source))
	case "result":
		l.result = ev.Result
		if l.result != nil {
			l.line("info", "deploy", fmt.Sprintf("status=ok url=%s alias=%s elapsed=%s",
				l.result.Deployment.URL, l.result.Deployment.Alias, l.elapsed()))
		}
	case "error":
		l.errEv = ev.Error
		l.handleError(ev.Error)
	}
}

func (l *Log) handleBuild(ev DeployEvent) {
	if ev.StepCount > l.stepCount {
		l.stepCount = ev.StepCount
	}
	switch ev.State {
	case "log":
		// One log line per non-empty raw chunk. Indent lightly so it
		// reads as nested under the surrounding step events.
		for _, chunk := range strings.Split(strings.TrimRight(ev.Log, "\n"), "\n") {
			if chunk == "" {
				continue
			}
			l.line("info", "build", fmt.Sprintf("step=%d/%d %s", ev.StepIndex, l.stepCount, chunk))
		}
	case "running":
		// Don't emit a running line for cached steps — they'll fire a
		// terminal `cached` event almost immediately and emitting both
		// would double the noise.
		l.line("info", "build", fmt.Sprintf("step=%d/%d running name=%s",
			ev.StepIndex, l.stepCount, ev.Step))
	case "done", "cached":
		state := ev.State
		l.line("info", "build", fmt.Sprintf("step=%d/%d %s name=%s elapsed=%s",
			ev.StepIndex, l.stepCount, state, ev.Step, formatElapsed(ev.ElapsedMs)))
	case "fail":
		l.line("error", "build", fmt.Sprintf("step=%d/%d fail name=%s elapsed=%s",
			ev.StepIndex, l.stepCount, ev.Step, formatElapsed(ev.ElapsedMs)))
	}
}

func (l *Log) handleError(e *DeployError) {
	if e == nil {
		return
	}
	if e.APIError != nil {
		l.line("error", "deploy", fmt.Sprintf("status=fail code=%s msg=%q", e.APIError.Code, e.APIError.Message))
	}
	// Only print the fenced BUILD OUTPUT block when there's real build
	// context — pre-build failures (auth, validation, archive size) get a
	// plain log line and the structured stderr event, no fake fence.
	hasBuildContext := e.FailedStep != "" || len(e.ParsedItems) > 0 ||
		e.BuildLogs != "" || (e.APIError != nil && e.APIError.Logs != "")
	if hasBuildContext {
		failedStep := e.FailedStep
		if failedStep == "" {
			failedStep = "build"
		}
		printlnTo(l.out, "")
		printlnTo(l.out, fmt.Sprintf("──── BUILD OUTPUT · step %d/%d (%s) ────", e.StepIndex, e.StepCount, failedStep))
		switch {
		case e.BuildLogs != "":
			printlnTo(l.out, strings.TrimRight(e.BuildLogs, "\n"))
		case len(e.ParsedItems) > 0:
			for _, p := range e.ParsedItems {
				printlnTo(l.out, fmt.Sprintf("%s:%d:%d: %s", p.File, p.Line, p.Col, p.Message))
			}
		case e.APIError != nil && e.APIError.Logs != "":
			printlnTo(l.out, strings.TrimRight(e.APIError.Logs, "\n"))
		}
		printlnTo(l.out, "──── END BUILD OUTPUT ────")
		printlnTo(l.out, "")
	}
	// Single-line structured event on stderr. Format mirrors the JSON
	// schema in cli-output.jsx:505 — keeps `jq` consumers (and coding
	// agents) cheap to write.
	if l.err != nil {
		_ = json.NewEncoder(l.err).Encode(structuredFailure(e))
	}
}

func (l *Log) OnDone() int {
	if l.errEv != nil {
		printlnTo(l.out, fmt.Sprintf("deploy failed  ·  %s  ·  %s", failedSummary(l.errEv), l.elapsed()))
		if l.errEv.FailedStep != "" {
			return 2
		}
		return 1
	}
	if l.result != nil {
		printlnTo(l.out, fmt.Sprintf("deploy ok  ·  %s  ·  %s", l.result.Deployment.URL, l.elapsed()))
	}
	return 0
}

func (l *Log) line(level, scope, msg string) {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	w := l.out
	if level == "error" || level == "warn" {
		// Errors go to stderr only when an err writer is set; otherwise
		// keep them on stdout so the line ordering stays intact.
		w = l.out
	}
	printlnTo(w, fmt.Sprintf("%s [%s] %-10s %s", ts, level, scope, msg))
}

func (l *Log) elapsed() string {
	return formatElapsed(time.Since(l.startedAt).Milliseconds())
}

func joinFields(extra string) string {
	if extra == "" {
		return ""
	}
	return " " + extra
}

func failedSummary(e *DeployError) string {
	if e == nil || e.APIError == nil {
		return "unknown"
	}
	if e.FailedStep != "" {
		return fmt.Sprintf("step %d/%d (%s)", e.StepIndex, e.StepCount, e.FailedStep)
	}
	return e.APIError.Code
}

// structuredFailure builds the JSON event the design ships on stderr —
// one line, schema stable, designed for `jq` and coding agents.
type structuredFailureEvent struct {
	Event       string             `json:"event"`
	App         string             `json:"app,omitempty"`
	Step        string             `json:"step,omitempty"`
	StepIndex   int                `json:"step_index,omitempty"`
	StepCount   int                `json:"step_count,omitempty"`
	ExitCode    int                `json:"exit_code"`
	Reason      string             `json:"reason,omitempty"`
	Message     string             `json:"message,omitempty"`
	Errors      []ParsedBuildError `json:"errors,omitempty"`
	RetryCmd    string             `json:"retry_cmd,omitempty"`
	RequestID   string             `json:"request_id,omitempty"`
	DeployID    string             `json:"deploy_id,omitempty"`
	APIErrCode  string             `json:"api_error_code,omitempty"`
}

func structuredFailure(e *DeployError) structuredFailureEvent {
	// exit_code must match what OnDone actually returns: 2 only for build
	// step failures, 1 for everything else (auth, validation, network).
	exitCode := 1
	if e.FailedStep != "" {
		exitCode = 2
	}
	out := structuredFailureEvent{
		Event:     "deploy.failed",
		Step:      e.FailedStep,
		StepIndex: e.StepIndex,
		StepCount: e.StepCount,
		ExitCode:  exitCode,
		Errors:    e.ParsedItems,
		RetryCmd:  e.RetryCmd,
	}
	if e.APIError != nil {
		out.APIErrCode = e.APIError.Code
		out.RequestID = e.APIError.RequestID
		out.DeployID = e.APIError.DeploymentID
		out.Reason = strings.ToLower(e.APIError.Code)
		out.Message = e.APIError.Message
	}
	return out
}
