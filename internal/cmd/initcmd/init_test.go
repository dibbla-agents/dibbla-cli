package initcmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type recorderRunner struct {
	calls [][]string
	// stepErrs[i] returns the error for the i-th call, or nil if i is OOR.
	stepErrs []error
}

func (r *recorderRunner) Run(name string, args ...string) error {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	idx := len(r.calls) - 1
	if idx < len(r.stepErrs) {
		return r.stepErrs[idx]
	}
	return nil
}

// resetFlags clears global flag state between tests.
func resetFlags() {
	flagYes = false
	flagSkipUpdate = false
	flagSkipSkill = false
	flagUser = false
	flagAPIURL = ""
	flagReLogin = false
}

func newCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	buf := &bytes.Buffer{}
	c.SetOut(buf)
	return c, buf
}

func TestOrchestrate_HappyPath_RunsAllThreeSteps(t *testing.T) {
	resetFlags()
	old := hasToken
	hasToken = func() bool { return false }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, buf := newCmd()
	if err := orchestrate(c, "/path/to/dibbla", r); err != nil {
		t.Fatalf("orchestrate returned error: %v", err)
	}

	if got, want := len(r.calls), 3; got != want {
		t.Fatalf("expected %d calls, got %d: %v", want, got, r.calls)
	}
	checkCall(t, r.calls[0], "/path/to/dibbla", "update", "--yes")
	checkCall(t, r.calls[1], "/path/to/dibbla", "login")
	checkCall(t, r.calls[2], "/path/to/dibbla", "skills", "install", "dibbla")

	out := buf.String()
	for _, want := range []string{"Step 1/3", "Step 2/3", "Step 3/3", "Setup complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nout: %s", want, out)
		}
	}
}

func TestOrchestrate_SkipsLoginWhenTokenAlreadyPresent(t *testing.T) {
	resetFlags()
	old := hasToken
	hasToken = func() bool { return true }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, buf := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}

	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "login" {
			t.Fatalf("login should have been skipped; calls: %v", r.calls)
		}
	}
	if !strings.Contains(buf.String(), "already configured") {
		t.Errorf("output should mention skip reason\nout: %s", buf.String())
	}
}

func TestOrchestrate_ReLoginForcesLoginEvenWithToken(t *testing.T) {
	resetFlags()
	flagReLogin = true
	old := hasToken
	hasToken = func() bool { return true }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, _ := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}
	found := false
	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "login" {
			found = true
		}
	}
	if !found {
		t.Errorf("--re-login should force login despite existing token")
	}
}

func TestOrchestrate_SkipUpdate(t *testing.T) {
	resetFlags()
	flagSkipUpdate = true
	old := hasToken
	hasToken = func() bool { return false }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, _ := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "update" {
			t.Fatalf("update should have been skipped; calls: %v", r.calls)
		}
	}
}

func TestOrchestrate_SkipSkill(t *testing.T) {
	resetFlags()
	flagSkipSkill = true
	old := hasToken
	hasToken = func() bool { return true } // also skip login
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, _ := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "skills" {
			t.Fatalf("skills install should have been skipped; calls: %v", r.calls)
		}
	}
}

func TestOrchestrate_UpdateFailureIsSoftFail(t *testing.T) {
	resetFlags()
	old := hasToken
	hasToken = func() bool { return true } // skip login
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{stepErrs: []error{errors.New("network down")}}
	c, buf := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("update failure should not propagate; got %v", err)
	}
	if !strings.Contains(buf.String(), "update step failed") {
		t.Errorf("expected warning in output\nout: %s", buf.String())
	}
}

func TestOrchestrate_LoginFailureIsHardFail(t *testing.T) {
	resetFlags()
	flagSkipUpdate = true
	old := hasToken
	hasToken = func() bool { return false }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{stepErrs: []error{errors.New("auth rejected")}}
	c, _ := newCmd()
	err := orchestrate(c, "/x/dibbla", r)
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("expected login error, got %v", err)
	}
	// Skill install must NOT have been attempted after login failure.
	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "skills" {
			t.Errorf("skills should not run after login failure")
		}
	}
}

func TestOrchestrate_SkillFailureIsSoftFail(t *testing.T) {
	resetFlags()
	flagSkipUpdate = true
	old := hasToken
	hasToken = func() bool { return true } // skip login
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{stepErrs: []error{errors.New("disk full")}}
	c, buf := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("skill failure should not propagate; got %v", err)
	}
	if !strings.Contains(buf.String(), "skills install step failed") {
		t.Errorf("expected warning in output\nout: %s", buf.String())
	}
}

func TestOrchestrate_UserFlagAddsFlagToSkillInstall(t *testing.T) {
	resetFlags()
	flagUser = true
	old := hasToken
	hasToken = func() bool { return true }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, _ := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}
	var skillsCall []string
	for _, call := range r.calls {
		if len(call) >= 2 && call[1] == "skills" {
			skillsCall = call
			break
		}
	}
	if skillsCall == nil {
		t.Fatal("skills install was not invoked")
	}
	checkCall(t, skillsCall, "/x/dibbla", "skills", "install", "dibbla", "--user")
}

func TestOrchestrate_APIURLForwardsToLogin(t *testing.T) {
	resetFlags()
	flagSkipUpdate = true
	flagSkipSkill = true
	flagAPIURL = "https://api.dibbla.net"
	old := hasToken
	hasToken = func() bool { return false }
	t.Cleanup(func() { hasToken = old })

	r := &recorderRunner{}
	c, _ := newCmd()
	if err := orchestrate(c, "/x/dibbla", r); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %v", len(r.calls), r.calls)
	}
	checkCall(t, r.calls[0], "/x/dibbla", "login", "--api-url", "https://api.dibbla.net")
}

func TestRegister_AddsInitCommand(t *testing.T) {
	root := &cobra.Command{Use: "dibbla"}
	Register(root)
	c, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatalf("init not found: %v", err)
	}
	if c.Use != "init" {
		t.Errorf("got Use=%q want init", c.Use)
	}
}

func checkCall(t *testing.T, got []string, want ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("call mismatch\n got: %v\nwant: %v", got, want)
	}
}
