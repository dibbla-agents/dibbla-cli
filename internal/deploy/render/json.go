package render

import (
	"encoding/json"
	"io"
	"time"
)

// JSONRenderer buffers events and emits a single structured JSON object
// on completion — success or failure. Mirrors the `dibbla deploy --json`
// output in the design (cli-output.jsx:532-541 and 504-505).
type JSONRenderer struct {
	out       io.Writer
	startedAt time.Time

	prevRevision string // best-effort, populated from rollout-start source
	result       *DeployResult
	errEv        *DeployError
}

func NewJSON(out io.Writer) *JSONRenderer {
	return &JSONRenderer{out: out, startedAt: time.Now()}
}

func (j *JSONRenderer) OnEvent(ev DeployEvent) {
	switch ev.Type {
	case "result":
		j.result = ev.Result
	case "error":
		j.errEv = ev.Error
	}
}

func (j *JSONRenderer) OnDone() int {
	enc := json.NewEncoder(j.out)
	switch {
	case j.errEv != nil:
		_ = enc.Encode(structuredFailure(j.errEv))
		if j.errEv.FailedStep != "" {
			return 2
		}
		return 1
	case j.result != nil:
		_ = enc.Encode(map[string]any{
			"ok":         true,
			"alias":      j.result.Deployment.Alias,
			"url":        j.result.Deployment.URL,
			"status":     j.result.Deployment.Status,
			"deploy_id":  j.result.Deployment.ID,
			"vcs_commit": j.result.VCSCommit,
			"elapsed_ms": time.Since(j.startedAt).Milliseconds(),
		})
	}
	return 0
}
