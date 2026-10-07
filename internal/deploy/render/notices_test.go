package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// successWithNotices is the shape the server sends when a deploy is live
// but something alongside it did not complete: version control, the
// support block, or — since SLC-0129 — Application Checks promotion.
func successWithNotices() DeployEvent {
	return DeployEvent{
		Type: "result",
		Result: &DeployResult{
			Status: "success",
			Deployment: ResultDeployment{
				ID: "dep_1", Alias: "shop", URL: "https://shop.dibbla.com", Status: "running",
			},
			ChecksNotice: "APPLICATION_CHECKS_UNAVAILABLE: Application Checks persistence is unavailable, so this revision's checks were not promoted. The deploy succeeded and the new revision is live; only Application Checks were not updated.",
			VCSError:     "git: could not write commit",
			VCSFiltered:  []string{".env", "node_modules/"},
		},
	}
}

// SLC-0129. The defect was a live deploy rendered as "✗ ... exit 1". The
// fix must not overshoot into the opposite failure: a success that quietly
// drops the reason the post-deploy work did not finish. Both halves are
// asserted here — exit 0 AND the notice actually reaching the user.
//
// vcs_error and vcs_filtered are in the same test because the CLI already
// dropped both on the floor before this change: the server has sent them
// since P-0021 and DeployResult had no field to receive them.
func TestRenderersSurfaceSuccessNotices(t *testing.T) {
	ev := successWithNotices()

	cases := []struct {
		name string
		run  func(out, errW *bytes.Buffer) int
	}{
		{"tty", func(out, _ *bytes.Buffer) int {
			r := NewTTY(out, false)
			r.OnEvent(ev)
			return r.OnDone()
		}},
		{"log", func(out, errW *bytes.Buffer) int {
			r := NewLog(out, errW)
			r.OnEvent(ev)
			return r.OnDone()
		}},
		{"quiet", func(out, _ *bytes.Buffer) int {
			r := NewQuiet(out)
			r.OnEvent(ev)
			return r.OnDone()
		}},
		{"json", func(out, _ *bytes.Buffer) int {
			r := NewJSON(out)
			r.OnEvent(ev)
			return r.OnDone()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errW bytes.Buffer
			code := tc.run(&out, &errW)

			if code != 0 {
				t.Fatalf("exit %d — the deploy succeeded; a post-deploy notice must never change the exit code", code)
			}
			combined := out.String() + errW.String()
			for _, want := range []string{"APPLICATION_CHECKS_UNAVAILABLE", "could not write commit"} {
				if !strings.Contains(combined, want) {
					t.Errorf("output never mentions %q — silence is the other half of this defect:\n%s", want, combined)
				}
			}
			if strings.Contains(strings.ToLower(combined), "deploy failed") {
				t.Errorf("a successful deploy must not read as failed:\n%s", combined)
			}
		})
	}
}

// The JSON renderer is what agents parse, so its keys are a contract.
func TestJSONRendererNoticeKeys(t *testing.T) {
	var out bytes.Buffer
	r := NewJSON(&out)
	r.OnEvent(successWithNotices())
	if code := r.OnDone(); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out.String())
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if s, _ := got["checks_notice"].(string); !strings.Contains(s, "APPLICATION_CHECKS_UNAVAILABLE") {
		t.Errorf("checks_notice = %v", got["checks_notice"])
	}
	if s, _ := got["vcs_error"].(string); s == "" {
		t.Errorf("vcs_error missing: %v", got)
	}
	if _, ok := got["vcs_filtered"]; !ok {
		t.Errorf("vcs_filtered missing: %v", got)
	}
}

// A success with nothing to report must stay byte-stable: no empty notice
// lines, no null keys for agents to trip over.
func TestRenderersOmitAbsentNotices(t *testing.T) {
	ev := DeployEvent{Type: "result", Result: &DeployResult{
		Status:     "success",
		Deployment: ResultDeployment{ID: "dep_1", Alias: "shop", URL: "https://shop.dibbla.com", Status: "running"},
	}}

	var out bytes.Buffer
	r := NewJSON(&out)
	r.OnEvent(ev)
	r.OnDone()
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"checks_notice", "vcs_error", "vcs_filtered", "env_warnings", "mcp_published", "mcp_notice"} {
		if _, present := got[k]; present {
			t.Errorf("%q present on a clean deploy: %v", k, got)
		}
	}

	var qout bytes.Buffer
	q := NewQuiet(&qout)
	q.OnEvent(ev)
	q.OnDone()
	if strings.Contains(qout.String(), "!") {
		t.Errorf("clean quiet output gained a notice marker: %q", qout.String())
	}
}

// envWarnings are deploy-api's sentences for two secret-looking dibbla.yaml
// environment: entries, as a person's deploy receives them (DIB-1339).
var envWarnings = []string{
	"STRIPE_SECRET_KEY has the name of a secret — consider making it a secret (dibbla.yaml environment is readable by anyone who can read the app's source)",
	"the value of WEBHOOK_URL is shaped like a credential — consider making it a secret (dibbla.yaml environment is readable by anyone who can read the app's source)",
}

func successWithEnvWarnings(warnings []string) DeployEvent {
	return DeployEvent{Type: "result", Result: &DeployResult{
		Status:      "success",
		Deployment:  ResultDeployment{ID: "dep_1", Alias: "shop", URL: "https://shop.dibbla.com", Status: "running"},
		EnvWarnings: warnings,
	}}
}

// The server warned and the CLI said nothing: a deploy that kept a secret in
// dibbla.yaml looked clean. Every renderer now prints each warning as a
// notice, then once how to move one into secrets — a secret entry request,
// not 'env promote', which refuses a dibbla.yaml entry.
func TestRenderersPrintEnvWarnings(t *testing.T) {
	const hint = "to move one into secrets: remove its line from dibbla.yaml, run dibbla secrets request NAME -d shop for a new value (the old one was readable), and deploy again"
	cases := []struct {
		name   string
		r      func(out *bytes.Buffer) Renderer
		prefix string // how the renderer marks a notice, before its text
	}{
		{"tty", func(out *bytes.Buffer) Renderer { return NewTTY(out, false) }, "  env · "},
		{"log", func(out *bytes.Buffer) Renderer { return NewLog(out, out) }, "[warn] env        "},
		{"quiet", func(out *bytes.Buffer) Renderer { return NewQuiet(out) }, "! env: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := tc.r(&out)
			r.OnEvent(successWithEnvWarnings(envWarnings))
			if code := r.OnDone(); code != 0 {
				t.Fatalf("exit %d — the deploy went ahead; a warning must not fail it", code)
			}
			got := out.String()
			for _, line := range append(append([]string{}, envWarnings...), hint) {
				if !strings.Contains(got, tc.prefix+line+"\n") {
					t.Errorf("missing %q:\n%s", tc.prefix+line, got)
				}
			}
			if strings.Count(got, "dibbla secrets request") != 1 {
				t.Errorf("the hint must be said once, under the warnings:\n%s", got)
			}
			if i, j := strings.Index(got, envWarnings[1]), strings.Index(got, hint); i < 0 || j < i {
				t.Errorf("the hint must follow the warnings:\n%s", got)
			}
			t.Logf("%s output:\n%s", tc.name, got)
		})
	}

	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		r := NewJSON(&out)
		r.OnEvent(successWithEnvWarnings(envWarnings))
		if code := r.OnDone(); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		var got struct {
			OK          bool     `json:"ok"`
			EnvWarnings []string `json:"env_warnings"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("not JSON: %v (%s)", err, out.String())
		}
		if !got.OK || strings.Join(got.EnvWarnings, "\n") != strings.Join(envWarnings, "\n") {
			t.Errorf("env_warnings must be the server's strings as sent: %s", out.String())
		}
		if strings.Contains(out.String(), "secrets request") {
			t.Errorf("--json carries the server's data, not the CLI's hint: %s", out.String())
		}
		t.Logf("json output:\n%s", out.String())
	})
}

// Without env_warnings the output is what it was: the warnings add lines and
// change nothing else. The TTY is compared byte for byte with the env lines
// taken out again.
func TestTTYEnvWarningsAddLinesAndNothingElse(t *testing.T) {
	render := func(warnings []string) string {
		var out bytes.Buffer
		r := NewTTY(&out, false)
		r.OnEvent(successWithEnvWarnings(warnings))
		r.OnDone()
		return out.String()
	}
	without, with := render(nil), render(envWarnings)
	if strings.Contains(without, "env ·") || strings.Contains(without, "dibbla.yaml") {
		t.Fatalf("no warnings, yet:\n%s", without)
	}
	var kept []string
	for _, line := range strings.SplitAfter(with, "\n") {
		if !strings.HasPrefix(line, "  env · ") {
			kept = append(kept, line)
		}
	}
	// The notice block opens with a blank line of its own; with no other
	// notice, that blank line goes too. It is the last run of three newlines
	// (the frame above the summary has its own).
	stripped := strings.Join(kept, "")
	if i := strings.LastIndex(stripped, "\n\n\n"); i >= 0 {
		stripped = stripped[:i] + stripped[i+1:]
	}
	if stripped != without {
		t.Fatalf("the warnings changed more than their own lines:\n--- without\n%s\n--- with, env lines removed\n%s", without, stripped)
	}
}
