package wf

import (
	"errors"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/spf13/cobra"
)

func TestRenderAPIError_ValidationFailureIsReadable(t *testing.T) {
	// The raw form is this JSON printed after "API error 422:", on one line.
	// For a workflow with several findings that is not something a person
	// reads — and unread validator output is what makes people guess at fixes.
	raw := `{"error":"Validation failed","details":[` +
		`{"rule":"UNSATISFIED_INPUT","message":"Input \"model\" on node \"agent\" has no value","location":"nodes[1].inputs.model"},` +
		`{"rule":"UNSATISFIED_INPUT","message":"Input \"prompt\" on node \"agent\" has no value","location":"nodes[1].inputs.prompt"},` +
		`{"rule":"INVALID_EDGE_FORMAT","message":"invalid edge format","location":"edges[0]"}]}`

	got := renderAPIError(&apiclient.APIError{StatusCode: 422, Message: raw}).Error()

	for _, want := range []string{
		"Validation failed (3 problems)",
		"UNSATISFIED_INPUT",
		"nodes[1].inputs.model",
		"INVALID_EDGE_FORMAT",
		"edges[0]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q:\n%s", want, got)
		}
	}
	// Findings are grouped, so each rule heading appears exactly once even
	// though UNSATISFIED_INPUT has two entries.
	if n := strings.Count(got, "UNSATISFIED_INPUT"); n != 1 {
		t.Errorf("expected one UNSATISFIED_INPUT heading, got %d:\n%s", n, got)
	}
	// Every rule that has a known remedy carries it.
	if !strings.Contains(got, "dibbla fn get") {
		t.Errorf("UNSATISFIED_INPUT should point at the command that lists required inputs:\n%s", got)
	}
}

func TestRenderAPIError_SingularProblemWording(t *testing.T) {
	raw := `{"error":"Validation failed","details":[{"rule":"INVALID_NAME","message":"bad"}]}`
	got := renderAPIError(&apiclient.APIError{StatusCode: 422, Message: raw}).Error()
	if !strings.Contains(got, "(1 problem)") {
		t.Errorf("expected singular wording, got: %s", got)
	}
}

func TestRenderAPIError_StatusGuidance(t *testing.T) {
	cases := map[int]string{
		404: "dibbla wf list",
		409: "dibbla wf update",
		412: "re-fetch",
		401: "dibbla login",
	}
	for status, want := range cases {
		got := renderAPIError(&apiclient.APIError{
			StatusCode: status,
			Message:    `{"error":"nope"}`,
		}).Error()
		if !strings.Contains(got, want) {
			t.Errorf("status %d guidance missing %q, got: %s", status, want, got)
		}
		if !strings.Contains(got, "nope") {
			t.Errorf("status %d dropped the server's message, got: %s", status, got)
		}
	}
}

func TestRenderAPIError_PassesThroughNonAPIErrors(t *testing.T) {
	// A DNS failure or an unreadable file must not be dressed up as an API
	// response.
	orig := errors.New("dial tcp: connection refused")
	if got := renderAPIError(orig); got != orig {
		t.Errorf("non-API error should pass through unchanged, got %v", got)
	}
}

func TestRenderAPIError_UnparseableBodyKeepsTheRawMessage(t *testing.T) {
	got := renderAPIError(&apiclient.APIError{StatusCode: 500, Message: "upstream exploded"}).Error()
	if !strings.Contains(got, "upstream exploded") {
		t.Errorf("raw message should survive, got: %s", got)
	}
	if !strings.Contains(got, "500") {
		t.Errorf("status should be visible when there is no guidance, got: %s", got)
	}
}

func TestExitCodeFor(t *testing.T) {
	// Distinct codes are the whole point: a CI step has to tell "the workflow
	// is invalid" from "the server was unreachable" without scraping stderr.
	cases := []struct {
		err  error
		want int
	}{
		{&apiclient.APIError{StatusCode: 422}, 5},
		{&apiclient.APIError{StatusCode: 404}, 4},
		{&apiclient.APIError{StatusCode: 409}, 6},
		{&apiclient.APIError{StatusCode: 401}, 3},
		{&validationFailure{msg: "invalid"}, 5},
		{errors.New("connection refused"), 1},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.err); got != c.want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestHarden_AttachesExitCodeAndSilencesUsage(t *testing.T) {
	child := &cobra.Command{
		Use:  "child",
		RunE: func(*cobra.Command, []string) error { return &apiclient.APIError{StatusCode: 404, Message: "gone"} },
	}
	parent := &cobra.Command{Use: "parent"}
	parent.AddCommand(child)
	harden(parent)

	if !child.SilenceUsage {
		t.Error("a runtime error must not dump the usage text over the message")
	}
	err := child.RunE(child, nil)
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) {
		t.Fatalf("hardened command should carry an exit code, got %T", err)
	}
	if coded.ExitCode() != 4 {
		t.Errorf("exit code = %d, want 4", coded.ExitCode())
	}
	if !strings.Contains(err.Error(), "dibbla wf list") {
		t.Errorf("error should be the rendered form, got: %v", err)
	}
}

func TestHarden_LeavesSuccessAlone(t *testing.T) {
	cmd := &cobra.Command{Use: "ok", RunE: func(*cobra.Command, []string) error { return nil }}
	harden(cmd)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("success must stay success, got %v", err)
	}
}

func TestRejectUnknownSubcommand_RedirectsToTheRealCommand(t *testing.T) {
	// `dibbla wf tools add …` used to print help and exit 0, which reads as
	// "the tool was added".
	cmd := &cobra.Command{Use: "workflows"}
	err := rejectUnknownSubcommand(cmd, []string{"tools", "add", "wf1", "agent", "tool"})
	if err == nil {
		t.Fatal("an unknown subcommand must be an error, not a help dump")
	}
	if !strings.Contains(err.Error(), "dibbla tools add wf1 agent tool") {
		t.Errorf("should suggest the real command, got: %v", err)
	}
}

func TestRejectUnknownSubcommand_UnknownName(t *testing.T) {
	cmd := &cobra.Command{Use: "workflows"}
	err := rejectUnknownSubcommand(cmd, []string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("expected an error naming the unknown subcommand, got: %v", err)
	}
}

func TestParseValidationDetails(t *testing.T) {
	// `wf validate` answers 200 with the verdict in the body, so the details
	// arrive already decoded rather than as raw JSON.
	raw := []interface{}{
		map[string]interface{}{"rule": "R1", "message": "m1", "location": "l1"},
		map[string]interface{}{"rule": "R2", "message": "m2"},
		"not a finding",
	}
	got := parseValidationDetails(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 findings (the malformed entry dropped), got %d", len(got))
	}
	if got[0].Rule != "R1" || got[0].Location != "l1" || got[1].Rule != "R2" {
		t.Errorf("decoded findings wrong: %+v", got)
	}
}

func TestParseFileContent_AcceptsBothFormats(t *testing.T) {
	var out map[string]interface{}
	if err := parseFileContent([]byte(`{"name":"x","nodes":[]}`), &out); err != nil {
		t.Fatalf("JSON should parse: %v", err)
	}
	if out["name"] != "x" {
		t.Errorf("JSON decode wrong: %v", out)
	}

	out = nil
	if err := parseFileContent([]byte("name: y\nnodes: []\n"), &out); err != nil {
		t.Fatalf("YAML should parse: %v", err)
	}
	if out["name"] != "y" {
		t.Errorf("YAML decode wrong: %v", out)
	}
}

func TestParseFileContent_ReportsMalformedInput(t *testing.T) {
	var out map[string]interface{}
	if err := parseFileContent([]byte("name: [unclosed\n"), &out); err == nil {
		t.Error("malformed YAML must be reported, not silently accepted")
	}
}
