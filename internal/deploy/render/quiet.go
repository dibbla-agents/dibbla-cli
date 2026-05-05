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
		fmt.Fprintf(q.out, "✓ %s  ·  %s  ·  %s\n",
			q.result.Deployment.Alias, q.result.Deployment.URL, elapsed)
	}
	return 0
}
