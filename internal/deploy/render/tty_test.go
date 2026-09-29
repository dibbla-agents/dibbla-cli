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
			APIError:   &APIError{Code: "BUILD_FAILED", Message: "go build returned exit code 2", Documentation: "https://docs.dibbla.com/reference/plans"},
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
		"Docs: https://docs.dibbla.com/reference/plans",
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

// scriptedCacheMiss is an app's first deploy as deploy-api streams it since
// DIB-1211: there is no registry cache yet, BuildKit's import step errors and
// the build goes on without it, so the step arrives as "warn", not "fail".
func scriptedCacheMiss(r Renderer) {
	const cacheImport = "importing cache manifest from localhost:5000/myapp-cache:latest"
	r.OnEvent(DeployEvent{Type: "deploy", State: "started"})
	r.OnEvent(DeployEvent{Type: "build", State: "running", Step: cacheImport, Name: cacheImport, StepIndex: 1, StepCount: 1})
	r.OnEvent(DeployEvent{Type: "build", State: "warn", Step: cacheImport, Name: cacheImport, StepIndex: 1, StepCount: 1, ElapsedMs: 102})
	r.OnEvent(DeployEvent{Type: "build", State: "running", Step: "copy-source", Name: "[3/3] COPY site/ /usr/share/nginx/html", StepIndex: 2, StepCount: 2})
	r.OnEvent(DeployEvent{Type: "build", State: "done", Step: "copy-source", Name: "[3/3] COPY site/ /usr/share/nginx/html", StepIndex: 2, StepCount: 2, ElapsedMs: 10})
	r.OnEvent(DeployEvent{Type: "rollout", State: "rollout-start", Source: "strategy=create"})
	r.OnEvent(DeployEvent{Type: "rollout", State: "rollout-done"})
	r.OnEvent(DeployEvent{
		Type: "result",
		Result: &DeployResult{
			Status:     "success",
			Deployment: ResultDeployment{ID: "dep_abc", Alias: "myapp", URL: "https://myapp.dibbla.app", Status: "running"},
		},
	})
}

// TestTTY_CacheMissIsAWarning: the warned step is a yellow "!", never a red
// "✗", and it counts as finished — the bar ended at "step 11 of 12" (92%)
// on a successful deploy when it was drawn as a failure (DIB-1211).
func TestTTY_CacheMissIsAWarning(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	scriptedCacheMiss(r)
	if got := r.OnDone(); got != 0 {
		t.Fatalf("OnDone exit code = %d, want 0", got)
	}
	out := buf.String()
	if strings.Contains(out, "✗") {
		t.Errorf("a cache miss must not draw a failed step\n--- output ---\n%s", out)
	}
	for _, want := range []string{
		"!  importing cache manifest from",
		"step 1 of 1", // counted as soon as it is warned, not only at the end
		"step 3 of 3",
		"DEPLOYED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n--- output ---\n%s", want, out)
		}
	}

	buf.Reset()
	scriptedCacheMiss(NewTTY(&buf, true))
	if colored := buf.String(); !strings.Contains(colored, colorWarn+colorBold+"!") || strings.Contains(colored, colorRed) {
		t.Errorf("the warned step must be painted --warn, and nothing red\n--- output ---\n%q", colored)
	}
}
