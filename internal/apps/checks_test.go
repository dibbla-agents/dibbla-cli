package apps

import (
	"testing"
)

func TestStatusErrorExitCode(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{401, 3},
		{403, 3},
		{404, 4},
		{400, 5}, // deploy-api request validation
		{422, 5}, // slim API request validation
		{409, 6},
		{408, 7},
		{500, 1},
		{503, 1},
	}
	for _, tc := range cases {
		err := &StatusError{Status: tc.status}
		if got := err.ExitCode(); got != tc.want {
			t.Errorf("status %d: exit %d, want %d", tc.status, got, tc.want)
		}
	}
}

func TestStatusErrorMessageCarriesCode(t *testing.T) {
	err := &StatusError{Status: 404, Code: "APPLICATION_CHECK_NOT_FOUND", Message: "Application Checks resource not found"}
	want := "API error 404 (APPLICATION_CHECK_NOT_FOUND): Application Checks resource not found"
	if err.Error() != want {
		t.Errorf("message %q, want %q", err.Error(), want)
	}
}

func TestCheckExecutionIsTerminal(t *testing.T) {
	terminal := []string{"pass", "fail", "error", "indeterminate", "canceled", "skipped_concurrent"}
	for _, status := range terminal {
		if !(&CheckExecution{Status: status}).IsTerminal() {
			t.Errorf("%q should be terminal", status)
		}
	}
	for _, status := range []string{"queued", "running", ""} {
		if (&CheckExecution{Status: status}).IsTerminal() {
			t.Errorf("%q should not be terminal", status)
		}
	}
}

func TestAliasAndCheckIDPatterns(t *testing.T) {
	validAliases := []string{"myapp", "a123", "expense-reporter"}
	for _, alias := range validAliases {
		if !AliasRe.MatchString(alias) {
			t.Errorf("alias %q should match", alias)
		}
	}
	invalidAliases := []string{"Myapp", "my_app", "a", "ab", "-app", "app-", "app.example.com", ""}
	for _, alias := range invalidAliases {
		if AliasRe.MatchString(alias) {
			t.Errorf("alias %q should not match", alias)
		}
	}
	validChecks := []string{"home-page", "a", "assistant-answer"}
	for _, id := range validChecks {
		if !CheckIDRe.MatchString(id) {
			t.Errorf("check id %q should match", id)
		}
	}
	invalidChecks := []string{"Home", "home_page", "", "-home"}
	for _, id := range invalidChecks {
		if CheckIDRe.MatchString(id) {
			t.Errorf("check id %q should not match", id)
		}
	}
}
