package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const registryMsg = "The platform's container registry is unavailable (responded 500 Internal Server Error). This is a problem on Dibbla's side, not in your code: you do not need to change anything. Your existing app was not touched and keeps running."

// The registry going down surfaces as a failed export-image step with a raw
// buildkit tail — the shape that used to exit 2 as if it were the
// customer's build. It must exit 20 and lead with the platform message.
func platformOutage(r Renderer) {
	r.OnEvent(DeployEvent{Type: "deploy"})
	r.OnEvent(DeployEvent{Type: "build", State: "running", Step: "export-image", StepIndex: 5, StepCount: 5})
	r.OnEvent(DeployEvent{
		Type: "error",
		Error: &DeployError{
			APIError:   &APIError{Code: "REGISTRY_UNAVAILABLE", Message: registryMsg, RequestID: "req_1"},
			StatusCode: 503,
			FailedStep: "export-image",
			StepIndex:  5,
			StepCount:  5,
			BuildLogs:  "error writing layer blob: unknown: unknown error",
		},
	})
}

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		e    *DeployError
		want int
	}{
		{"nil", nil, 0},
		{"build step", &DeployError{APIError: &APIError{Code: "BUILD_FAILED"}, FailedStep: "go-build"}, 2},
		{"pre-build", &DeployError{APIError: &APIError{Code: "ALIAS_EXISTS"}}, 1},
		{"registry down with a failed step", &DeployError{APIError: &APIError{Code: "REGISTRY_UNAVAILABLE"}, FailedStep: "export-image"}, 20},
		{"buildkit down without a step", &DeployError{APIError: &APIError{Code: "BUILD_SERVICE_UNAVAILABLE"}}, 20},
	}
	for _, tc := range cases {
		if got := exitCodeFor(tc.e); got != tc.want {
			t.Errorf("%s: exit %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPlatformOutage_TTY(t *testing.T) {
	var buf bytes.Buffer
	r := NewTTY(&buf, false)
	platformOutage(r)
	if code := r.OnDone(); code != ExitPlatformUnavailable {
		t.Fatalf("OnDone = %d, want %d", code, ExitPlatformUnavailable)
	}
	out := buf.String()
	if !strings.Contains(out, "REGISTRY_UNAVAILABLE") || !strings.Contains(out, "not in your code") {
		t.Errorf("expected the platform message, got:\n%s", out)
	}
	if strings.Contains(out, "BUILD OUTPUT") || strings.Contains(out, "unknown: unknown error") {
		t.Errorf("a platform outage must not render the build fence:\n%s", out)
	}
}

func TestPlatformOutage_Log(t *testing.T) {
	var out, errOut bytes.Buffer
	r := NewLog(&out, &errOut)
	platformOutage(r)
	if code := r.OnDone(); code != ExitPlatformUnavailable {
		t.Fatalf("OnDone = %d, want %d", code, ExitPlatformUnavailable)
	}
	if strings.Contains(out.String(), "BUILD OUTPUT") {
		t.Errorf("a platform outage must not render the build fence:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "deploy failed  ·  REGISTRY_UNAVAILABLE") {
		t.Errorf("summary should name the code, got:\n%s", out.String())
	}
	var ev structuredFailureEvent
	if err := json.Unmarshal(errOut.Bytes(), &ev); err != nil {
		t.Fatalf("stderr is not one JSON event: %v\n%s", err, errOut.String())
	}
	if ev.ExitCode != ExitPlatformUnavailable || ev.APIErrCode != "REGISTRY_UNAVAILABLE" {
		t.Errorf("structured event = %+v", ev)
	}
}

func TestPlatformOutage_QuietAndJSON(t *testing.T) {
	var q bytes.Buffer
	quiet := NewQuiet(&q)
	platformOutage(quiet)
	if code := quiet.OnDone(); code != ExitPlatformUnavailable {
		t.Fatalf("quiet OnDone = %d", code)
	}
	if !strings.HasPrefix(q.String(), "✗ REGISTRY_UNAVAILABLE: The platform's container registry") {
		t.Errorf("quiet line = %q", q.String())
	}

	var j bytes.Buffer
	js := NewJSON(&j)
	platformOutage(js)
	if code := js.OnDone(); code != ExitPlatformUnavailable {
		t.Fatalf("json OnDone = %d", code)
	}
	var ev structuredFailureEvent
	if err := json.Unmarshal(j.Bytes(), &ev); err != nil || ev.ExitCode != ExitPlatformUnavailable {
		t.Errorf("json event = %+v (%v)", ev, err)
	}
}
