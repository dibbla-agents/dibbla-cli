package wf

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/spf13/cobra"
)

// validationError is one entry from the slim API's validator.
type validationError struct {
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
}

// errorBody is the shape the slim API returns for a failed write. `details`
// carries validator output; `error` is the human summary.
type errorBody struct {
	Error   string            `json:"error"`
	Details []validationError `json:"details"`
}

// renderValidationErrors formats validator output the way a person reads it:
// grouped by rule, location first, with the offending YAML path intact so it
// can be pasted into an editor's go-to-line.
//
// The raw form is a single-line JSON blob printed after "API error 422:",
// which for a workflow with a dozen findings is unreadable — and unreadable
// validator output is what pushes people to guess at fixes instead of reading
// them.
func renderValidationErrors(summary string, details []validationError) string {
	var b strings.Builder
	if summary == "" {
		summary = "Validation failed"
	}
	fmt.Fprintf(&b, "%s (%d %s)\n", summary, len(details), plural(len(details), "problem", "problems"))

	byRule := map[string][]validationError{}
	var rules []string
	for _, d := range details {
		if _, seen := byRule[d.Rule]; !seen {
			rules = append(rules, d.Rule)
		}
		byRule[d.Rule] = append(byRule[d.Rule], d)
	}
	sort.Strings(rules)

	for _, rule := range rules {
		fmt.Fprintf(&b, "\n  %s\n", rule)
		for _, d := range byRule[rule] {
			if d.Location != "" {
				fmt.Fprintf(&b, "    %s\n      %s\n", d.Location, d.Message)
			} else {
				fmt.Fprintf(&b, "    %s\n", d.Message)
			}
		}
		if hint := ruleHint(rule); hint != "" {
			fmt.Fprintf(&b, "    → %s\n", hint)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ruleHint maps a validator rule to the action that resolves it. The rule name
// alone tells you what is wrong but not what to do about it, and the two most
// common rules (UNSATISFIED_INPUT, UNKNOWN_PORT) are both resolved by reading
// the function's schema — which is one command away but not an obvious one.
func ruleHint(rule string) string {
	switch rule {
	case "UNSATISFIED_INPUT":
		return "give the input a value, or wire an edge into it. `dibbla fn get <server> <function>` lists which inputs are required"
	case "UNKNOWN_PORT", "UNKNOWN_FUNCTION":
		return "check the names with `dibbla fn get <server> <function>`"
	case "INVALID_ENUM_VALUE":
		return "allowed values are listed under the input in `dibbla fn get <server> <function>`"
	case "TOOLS_NOT_SUPPORTED":
		return "only functions tagged accepts_tools can take a `tools:` list — `dibbla fn list --tag accepts_tools`"
	case "CAPABILITY_NOT_SUPPORTED":
		return "the function does not declare that capability; `dibbla fn get <server> <function>` lists the ones it has"
	case "INVALID_EDGE_FORMAT":
		return `edges are "src.port -> tgt.port" — the spaces around the arrow are required`
	case "DUPLICATE_INPUT_EDGE":
		return "an input takes at most one incoming edge; one output may fan out to many"
	case "CYCLE_DETECTED":
		return "a node cannot be both a tool of an agent and a data source feeding that agent — pick one role"
	case "INVALID_LINK":
		return "`linked_to` must name an `api` node in the same workflow"
	case "SELF_REFERENTIAL_TOOL":
		return "remove the node's own id from its `tools:` list"
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// statusGuidance explains a status code in terms of what the caller should do,
// for the codes the slim API actually uses.
func statusGuidance(status int) string {
	switch status {
	case 401, 403:
		return "not authorised — run `dibbla login`, or check DIBBLA_API_TOKEN"
	case 404:
		return "not found — `dibbla wf list` shows the workflows in this organization"
	case 409:
		return "already exists — use `dibbla wf update <name> -f <file>` to replace it"
	case 412:
		return "the workflow changed on the server since this copy was fetched — re-fetch with `dibbla wf get`, reapply, and retry (or pass --force)"
	}
	return ""
}

// renderAPIError turns a transport-level APIError into something worth
// printing. Validation failures become a grouped report; everything else keeps
// the server's message but gains the next action.
func renderAPIError(err error) error {
	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	var body errorBody
	if json.Unmarshal([]byte(apiErr.Message), &body) == nil && len(body.Details) > 0 {
		return errors.New(renderValidationErrors(body.Error, body.Details))
	}

	msg := strings.TrimSpace(apiErr.Message)
	if body.Error != "" {
		msg = body.Error
	}
	if guidance := statusGuidance(apiErr.StatusCode); guidance != "" {
		if msg == "" {
			return fmt.Errorf("%s", guidance)
		}
		return fmt.Errorf("%s\n  → %s", msg, guidance)
	}
	if msg == "" {
		return fmt.Errorf("request failed with HTTP %d", apiErr.StatusCode)
	}
	return fmt.Errorf("%s (HTTP %d)", msg, apiErr.StatusCode)
}

// exitCodeFor maps an error to the process exit status, so a script can tell a
// validation failure (5) from a missing workflow (4) from a network problem
// (1) without scraping stderr.
func exitCodeFor(err error) int {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		return apiclient.ExitCodeForStatus(apiErr.StatusCode)
	}
	var ve *validationFailure
	if errors.As(err, &ve) {
		return 5
	}
	return 1
}

// validationFailure marks a `wf validate` run that the server answered with
// HTTP 200 and `valid: false`. It used to exit 0, so a CI step that validated
// a broken workflow passed.
type validationFailure struct{ msg string }

func (e *validationFailure) Error() string { return e.msg }

// harden applies uniform error handling to a command tree: no usage dump on a
// runtime error, rendered API errors, and a meaningful exit code.
func harden(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := run(c, args)
			if err == nil {
				return nil
			}
			code := exitCodeFor(err)
			rendered := renderAPIError(err)
			return &exitError{err: rendered, code: code}
		}
	}
	for _, child := range cmd.Commands() {
		harden(child)
	}
}

// exitError carries the process exit code alongside the message.
type exitError struct {
	err  error
	code int
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) ExitCode() int { return e.code }
func (e *exitError) Unwrap() error { return e.err }
