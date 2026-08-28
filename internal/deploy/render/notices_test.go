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
	for _, k := range []string{"checks_notice", "vcs_error", "vcs_filtered"} {
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
