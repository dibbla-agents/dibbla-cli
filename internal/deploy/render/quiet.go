package render

import (
	"fmt"
	"io"
	"time"
)

// Quiet renders nothing during the build and emits a single line on
// completion — the form `✓ <id> · <url> · <elapsed>` from the design.
// Useful inside scripts and pre-commit hooks where the output is
// post-processed.
type Quiet struct {
	out       io.Writer
	startedAt time.Time
	result    *DeployResult
	errEv     *DeployError
}

func NewQuiet(out io.Writer) *Quiet {
	return &Quiet{out: out, startedAt: time.Now()}
}

func (q *Quiet) OnEvent(ev DeployEvent) {
	switch ev.Type {
	case "result":
		q.result = ev.Result
	case "error":
		q.errEv = ev.Error
	}
}

func (q *Quiet) OnDone() int {
	elapsed := formatElapsed(time.Since(q.startedAt).Milliseconds())
	switch {
	case q.errEv != nil:
		code := "ERROR"
		msg := "deploy failed"
		if q.errEv.APIError != nil {
			code = q.errEv.APIError.Code
			msg = q.errEv.APIError.Message
		}
		fmt.Fprintf(q.out, "✗ %s: %s\n", code, msg)
		if q.errEv.FailedStep != "" {
			return 2
		}
		return 1
	case q.result != nil:
		// Append a "(N services)" suffix for multi-service deploys so quiet
		// output reflects the new shape; legacy single-app deploys keep the
		// byte-stable line.
		suffix := ""
		if n := countDisplayServices(q.result.Deployment.Services); n > 0 {
			suffix = fmt.Sprintf("  ·  %d services", n)
		}
		fmt.Fprintf(q.out, "✓ %s  ·  %s  ·  %s%s\n",
			q.result.Deployment.Alias, q.result.Deployment.URL, elapsed, suffix)
	}
	return 0
}

// countDisplayServices returns the count of services to surface in user
// output. Returns 0 for empty/legacy-shape (so the renderer suppresses the
// suffix and stays byte-stable for legacy deploys).
func countDisplayServices(svcs []ServiceView) int {
	if len(svcs) == 0 {
		return 0
	}
	if len(svcs) == 1 && svcs[0].Name == "app" {
		return 0
	}
	return len(svcs)
}
