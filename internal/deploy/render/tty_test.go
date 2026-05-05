package render

import (
	"bytes"
	"strings"
	"testing"
)

// scriptedHappy walks a renderer through a successful deploy. Mirrors what
// the server emits for a small build with three steps + rollout.
func scriptedHappy(r Renderer) {
	r.OnEvent(DeployEvent{Type: "deploy", State: "started", Source: "deployment_id=abc"})
	r.OnEvent(DeployEvent{Type: "build", State: "running", Step: "go-build", Name: "RUN go build", StepIndex: 1, StepCount: 3})
	r.OnEvent(DeployEvent{Type: "build", State: "done", Step: "go-build", Name: "RUN go build", StepIndex: 1, StepCount: 3, ElapsedMs: 14700})
	r.OnEvent(DeployEvent{Type: "build", State: "done", Step: "package", Name: "package layer", StepIndex: 2, StepCount: 3, ElapsedMs: 2400})
	r.OnEvent(DeployEvent{Type: "build", State: "done", Step: "push", Name: "push", StepIndex: 3, StepCount: 3, ElapsedMs: 6000})
	r.OnEvent(DeployEvent{Type: "rollout", State: "rollout-start", Source: "strategy=rolling"})
	r.OnEvent(DeployEvent{Type: "rollout", State: "rollout-done"})
	r.OnEvent(DeployEvent{
		Type: "result",
		Result: &DeployResult{
			Status: "success",
			Deployment: ResultDeployment{
				ID:     "dep_abc",
				Alias:  "analytics-api",
				URL:    "https://analytics-api.dibbla.com",
				Status: "running",
			},
		},
	})
}

func scriptedFailure(r Renderer) {
	r.OnEvent(DeployEvent{Type: "deploy", State: "started"})
	r.OnEvent(DeployEvent{Type: "build", State: "running", Step: "go-build", Name: "RUN go build", StepIndex: 1, StepCount: 5})
	r.OnEvent(DeployEvent{Type: "build", State: "fail", Step: "go-build", Name: "RUN go build", StepIndex: 1, StepCount: 5, ElapsedMs: 18200})
	r.OnEvent(DeployEvent{
		Type: "error",
		Error: &DeployError{
			APIError:   &APIError{Code: "BUILD_FAILED", Message: "go build returned exit code 2"},
			StatusCode: 422,
			FailedStep: "go-build",
			StepIndex:  1,
			StepCount:  5,
			ParsedItems: []ParsedBuildError{
				{File: "internal/api/router.go", Line: 42, Col: 18, Message: "undefined: store.NewPostgres"},
			},
			RetryCmd: "dibbla deploy --update -a analytics-api",
		},
	})
}

func TestTTY_Happy(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false) // ANSI off → deterministic golden text
	scriptedHappy(r)
	if got := r.OnDone(); got != 0 {
		t.Fatalf("OnDone exit code = %d, want 0", got)
	}
	out := buf.String()
	for _, want := range []string{
		"DEPLOYED",
		"https://analytics-api.dibbla.com",
		"analytics-api",
		"RUN go build",
		"step 4 of 4", // 4 = 3 build + 1 rollout pseudo-step
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestTTY_Failure(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	scriptedFailure(r)
	if got := r.OnDone(); got != 2 {
		t.Fatalf("OnDone exit code = %d, want 2 (build failure)", got)
	}
	out := buf.String()
	for _, want := range []string{
		"BUILD OUTPUT",
		"step 1/5",
		"go-build",
		"undefined: store.NewPostgres",
		"BUILD_FAILED",
		"re-run · dibbla deploy --update -a analytics-api",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestTTY_FailureFallsBackToRawLogs(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	r.OnEvent(DeployEvent{Type: "deploy", State: "started"})
	r.OnEvent(DeployEvent{
		Type: "error",
		Error: &DeployError{
			APIError:   &APIError{Code: "BUILD_FAILED", Message: "boom"},
			BuildLogs:  "Dockerfile syntax error: unknown directive RUNN\n",
			FailedStep: "snapshot-source",
			StepIndex:  1,
			StepCount:  1,
		},
	})
	out := buf.String()
	if !strings.Contains(out, "unknown directive RUNN") {
		t.Errorf("expected raw build log fallback in output\n--- output ---\n%s", out)
	}
}
