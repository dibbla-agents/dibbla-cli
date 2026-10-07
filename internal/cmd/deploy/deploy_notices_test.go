package deploy

// DIB-1353: a deploy started by `git push` — `dibbla deploy` in a linked
// folder, or a plain push followed with `dibbla deploy status --follow` —
// ends with the same notices a direct deploy's response prints, read from the
// operation's result. The trial heads-up is the exception on purpose: the
// push's remote: lines carry it, and it is printed once.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

const trialHeadsUp = "Heads-up: your free trial ends in 3 days (Oct 10)."

// pushNotices is a finished push deploy's result as deploy-api records it:
// the deployment plus the notices HandleDeploy's response carried. It also
// carries a trial_warning, which deploy-api does not put there — so the test
// proves the CLI would not print a second copy even if one arrived.
func pushNotices() map[string]any {
	return map[string]any{
		"result": "deployed", "alias": "shop", "url": "https://shop.example", "deployment_id": "dep_1",
		"status": "running", "commit_sha": strings.Repeat("a", 40),
		"env_warnings":   []string{"API_TOKEN has the name of a secret — consider making it a secret (dibbla.yaml environment is readable by anyone who can read the app's source)"},
		"vcs_filtered":   []string{".env", "node_modules/"},
		"support_notice": "dibbla.yaml's support: block overrides the console setting",
		"checks_notice":  "Application Checks could not be promoted onto this revision",
		"mcp_published":  []string{"shop-tools"},
		"trial_warning":  map[string]any{"days_left": 3, "ends_at": "2026-10-10T00:00:00Z", "message": trialHeadsUp},
	}
}

// directResult is the same deploy as a direct `dibbla deploy` decodes it from
// the server's response — what the push's output has to read like.
func directResult(t *testing.T) *render.DeployResult {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"status":         "success",
		"deployment":     map[string]any{"id": "dep_1", "alias": "shop", "url": "https://shop.example", "status": "running"},
		"env_warnings":   pushNotices()["env_warnings"],
		"vcs_filtered":   pushNotices()["vcs_filtered"],
		"support_notice": pushNotices()["support_notice"],
		"checks_notice":  pushNotices()["checks_notice"],
		"mcp_published":  pushNotices()["mcp_published"],
	})
	var res render.DeployResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	res.MCPAddresses = map[string]string{"shop-tools": "https://mcp.example/platform/servers/shop-tools"}
	return &res
}

// noticeLines are the log renderer's notice and MCP lines without their
// timestamps: what must read the same on both paths.
func noticeLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, " ["); i >= 0 && (strings.Contains(line, "[warn]") || strings.Contains(line, " mcp ")) {
			lines = append(lines, line[i+1:])
		}
	}
	return lines
}

func stubOperationMCPAddress(t *testing.T) {
	t.Helper()
	orig := operationMCPAddress
	operationMCPAddress = func(server string) string { return "https://mcp.example/platform/servers/" + server }
	t.Cleanup(func() { operationMCPAddress = orig })
}

func TestPushDeployEndsWithTheNoticesADirectDeployPrints(t *testing.T) {
	gitEnv(t)
	stubOperationMCPAddress(t)
	work, _ := linkedRepo(t)
	os.WriteFile(filepath.Join(work, "app.py"), []byte("print('v2')\n"), 0o644)
	stubPushOutput(t, "remote: Dibbla: deploying 4f2a9c1e0b7d to shop\n"+
		"remote:   operation: deployment:op-1\n"+
		"remote:   follow:    dibbla deploy status deployment:op-1 --follow\n"+
		"remote: \n"+
		"remote: "+trialHeadsUp+"\n")
	srv, _ := operationServer(t, map[string]any{"phase": "succeeded", "terminal": true,
		"finished_at": "2026-09-19T10:01:00Z", "result": pushNotices()})

	var stdout, stderr bytes.Buffer
	code := runGitDeploy(input(work, "feat: v2", false), &stdout, &stderr, func(id string) int {
		return runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", id, true, false, render.NewLog(&stdout, &stderr), time.Millisecond)
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	var direct bytes.Buffer
	r := render.NewLog(&direct, &direct)
	r.OnEvent(render.DeployEvent{Type: "result", Result: directResult(t)})
	want := noticeLines(direct.String())
	if len(want) < 6 {
		t.Fatalf("the direct deploy printed too few notice lines to compare:\n%s", direct.String())
	}
	got := noticeLines(stdout.String())
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the push deploy's notices differ from a direct deploy's\npush:\n%s\ndirect:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The heads-up came on the push's remote: lines; it is not printed again.
	all := stdout.String() + stderr.String()
	if n := strings.Count(all, trialHeadsUp); n != 1 {
		t.Errorf("the trial heads-up is printed %d times, want once:\n%s", n, all)
	}
}

// --json keeps its promise of one object, and that object carries the notices
// under the names a direct deploy's --json uses.
func TestPushDeployJSONCarriesTheNotices(t *testing.T) {
	stubOperationMCPAddress(t)
	srv, _ := operationServer(t, map[string]any{"phase": "succeeded", "terminal": true,
		"finished_at": "2026-09-19T10:01:00Z", "result": pushNotices()})
	var stdout, stderr bytes.Buffer
	if code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", true, true, render.NewJSON(&stdout), time.Millisecond); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout.String())
	}
	for _, key := range []string{"env_warnings", "vcs_filtered", "support_notice", "checks_notice", "mcp_published"} {
		if doc[key] == nil {
			t.Errorf("--json lacks %s: %s", key, stdout.String())
		}
	}
	if doc["trial_warning"] != nil {
		t.Errorf("--json repeats the trial heads-up the push already printed: %s", stdout.String())
	}
}

// Reading a finished push deploy without --follow shows the same caveats.
func TestDeployStatusOfAFinishedPushShowsItsNotices(t *testing.T) {
	srv, _ := operationServer(t, map[string]any{"phase": "succeeded", "terminal": true,
		"finished_at": "2026-09-19T10:01:00Z", "result": pushNotices()})
	var stdout, stderr bytes.Buffer
	// operationServer answers "running" on its first read; the second read
	// is the finished deploy.
	_ = runDeployStatusCore(&bytes.Buffer{}, &stderr, srv.URL, "tok", "deployment:op-1", false, false, nil, time.Millisecond)
	if code := runDeployStatusCore(&stdout, &stderr, srv.URL, "tok", "deployment:op-1", false, false, nil, time.Millisecond); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{
		"! env: API_TOKEN has the name of a secret",
		"! env: to move one into secrets: remove its line from dibbla.yaml",
		"! vcs: 2 path(s) excluded from version control: .env, node_modules/",
		"! support: dibbla.yaml's support: block overrides the console setting",
		"! checks: Application Checks could not be promoted",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), trialHeadsUp) {
		t.Errorf("status repeats the trial heads-up:\n%s", stdout.String())
	}
}
