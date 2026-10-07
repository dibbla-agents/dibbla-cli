package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
)

// `dibbla secrets request` (DIB-1343) and `dibbla env promote` (DIB-1340): a
// secret's value goes from the person's browser into Dibbla, never through
// this terminal. These tests pin the request the CLI sends (and that it never
// carries a value), what it prints for the person, the --wait loop on an
// injected clock, --status, the refusals, and `secrets set`'s new manners: a
// warning for a value on the command line, and a hidden prompt at a terminal.

var entryT0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

const entryID = "sreq_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// entryDoc is deploy-api's view of a request in the given state.
func entryDoc(state string, extra map[string]any) string {
	doc := map[string]any{
		"request_id": entryID, "state": state, "name": "STRIPE_API_KEY", "deployment_alias": "shop",
		"title": "Stripe secret key", "explanation": "Shop charges cards with it.", "replaces_existing": false,
		"entry_url":       "https://console.dibbla.com/connector/secrets/" + entryID,
		"organization_id": "org-1", "organization_slug": "acme", "requesting_user_id": "u-1", "client_id": "dibbla-cli",
		"created_at": entryT0.Format(time.RFC3339), "expires_at": entryT0.Add(15 * time.Minute).Format(time.RFC3339),
	}
	if state == "completed" {
		doc["completed_at"] = entryT0.Add(4 * time.Minute).Format(time.RFC3339)
	}
	for k, v := range extra {
		doc[k] = v
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

type entryReply struct {
	status int
	body   string
}

// entryServer answers the create with create, and each status read with the
// next of statuses (the last one repeats). It records every request.
type entryServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
	reads    int
}

func newEntryServer(t *testing.T, create entryReply, statuses ...entryReply) *entryServer {
	t.Helper()
	s := &entryServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(body)})
		reply := create
		if r.Method == http.MethodGet {
			if len(statuses) == 0 {
				reply = entryReply{404, `{"status":"error","error":{"code":"NOT_FOUND","message":"resource not found"}}`}
			} else {
				reply = statuses[min(s.reads, len(statuses)-1)]
			}
			s.reads++
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *entryServer) created(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
				t.Fatalf("create body %q: %v", r.Body, err)
			}
			return body
		}
	}
	t.Fatal("no create request was made")
	return nil
}

// fakeClock is the clock --wait runs on: Sleep advances Now.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
}

func runEntry(t *testing.T, srv *entryServer, in entryRequestInput) (code int, stdout, stderr string, clock *fakeClock) {
	t.Helper()
	clock = &fakeClock{now: entryT0}
	in.APIURL, in.APIToken, in.Now, in.Sleep = srv.URL, "tok", clock.Now, clock.Sleep
	var out, errb bytes.Buffer
	if in.Promote {
		code = runEnvPromoteCore(&out, &errb, in)
	} else {
		code = runSecretsRequestCore(&out, &errb, in)
	}
	return code, out.String(), errb.String(), clock
}

func TestSecretsRequestCreatesTheRequestAndPrintsTheLink(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", map[string]any{"replaces_existing": true})})
	code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{
		Name: "STRIPE_API_KEY", Deployment: "shop", Service: "web",
		Title: "  Stripe secret key ", Why: "Shop charges cards with it.\r\nStripe dashboard → Developers → API keys.",
	})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if len(srv.requests) != 1 || srv.requests[0].Path != "/api/deploy/secret-entry-requests" || srv.requests[0].Auth != "Bearer tok" {
		t.Fatalf("requests: %+v", srv.requests)
	}
	body := srv.created(t)
	want := map[string]any{"name": "STRIPE_API_KEY", "deployment_alias": "shop", "service_name": "web",
		"title": "Stripe secret key", "explanation": "Shop charges cards with it.\nStripe dashboard → Developers → API keys."}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %#v, want %#v", k, body[k], v)
		}
	}
	for _, k := range []string{"value", "promote_env"} {
		if _, ok := body[k]; ok {
			t.Errorf("a plain request carries %q: %v", k, body)
		}
	}
	for _, w := range []string{
		"Secret entry request for STRIPE_API_KEY (deployment shop).",
		"Open this link and enter the value; it never passes through this terminal:",
		"https://console.dibbla.com/connector/secrets/" + entryID,
		"Request:  " + entryID,
		"Expires:  2026-10-07 12:15 UTC (in 15 min)",
		"Replaces: the current value of STRIPE_API_KEY.",
		"Check:    dibbla secrets request --status " + entryID,
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr: %s", stderr)
	}
	t.Logf("secrets request output:\n%s", stdout)
}

func TestSecretsRequestJSONIsTheServersDocument(t *testing.T) {
	doc := entryDoc("pending", nil)
	srv := newEntryServer(t, entryReply{201, doc + "\n"})
	code, stdout, _, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", JSON: true})
	if code != 0 || stdout != doc+"\n" {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
}

func TestSecretsRequestWaitPollsUntilTheValueIsEntered(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)},
		entryReply{200, entryDoc("pending", nil)},
		entryReply{502, `bad gateway`}, // one blip on a long wait is not the end of it
		entryReply{200, entryDoc("saving", nil)},
		entryReply{200, entryDoc("completed", nil)},
	)
	code, stdout, stderr, clock := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if srv.reads != 4 || len(clock.slept) != 4 {
		t.Errorf("reads %d, sleeps %v", srv.reads, clock.slept)
	}
	for _, d := range clock.slept {
		if d != 3*time.Second {
			t.Errorf("slept %v, want 3s", d)
		}
	}
	for _, w := range []string{"https://console.dibbla.com/connector/secrets/", "Waiting for the value to be entered on the page", "The value of STRIPE_API_KEY (deployment shop) was entered and saved."} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	if srv.requests[1].Path != "/api/deploy/secret-entry-requests/"+entryID+"/status" {
		t.Errorf("status path %s", srv.requests[1].Path)
	}
	t.Logf("secrets request --wait output:\n%s", stdout)
}

func TestSecretsRequestWaitEndsWhenTheRequestExpires(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)},
		entryReply{200, entryDoc("pending", nil)},
		entryReply{200, entryDoc("expired", nil)},
	)
	code, stdout, _, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true})
	if code != 7 || !strings.Contains(stdout, "expired before a value was entered. Nothing was saved; run the command again for a new link.") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
}

func TestSecretsRequestWaitGivesUpPastTheExpiry(t *testing.T) {
	// A server that never says anything but pending: --wait stops a minute
	// after the request's own expiry instead of reading forever.
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)}, entryReply{200, entryDoc("pending", nil)})
	code, _, stderr, clock := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true})
	if code != 7 || !strings.Contains(stderr, "stopped waiting for request "+entryID) || !strings.Contains(stderr, "--status "+entryID) {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if limit := entryT0.Add(16*time.Minute + 3*time.Second); clock.now.After(limit) {
		t.Errorf("waited until %v, past %v", clock.now, limit)
	}
}

func TestSecretsRequestWaitReportsACancelledRequest(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)}, entryReply{200, entryDoc("cancelled", nil)})
	code, stdout, _, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true})
	if code != 1 || !strings.Contains(stdout, "was cancelled on the page. Nothing was saved.") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
}

func TestSecretsRequestWaitStopsAfterRepeatedFailures(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)}, entryReply{503, `{"status":"error","error":{"code":"INTERNAL_ERROR","message":"Secret entry requests are temporarily unavailable."}}`})
	code, _, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true})
	if code != 1 || srv.reads != entryPollFailures || !strings.Contains(stderr, "temporarily unavailable") {
		t.Errorf("exit %d after %d reads: %s", code, srv.reads, stderr)
	}
}

func TestSecretsRequestWaitJSONPrintsTheRequestAsCreatedAndAsItEnded(t *testing.T) {
	created, done := entryDoc("pending", nil), entryDoc("completed", nil)
	srv := newEntryServer(t, entryReply{201, created}, entryReply{200, done})
	code, stdout, _, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop", Wait: true, JSON: true})
	if code != 0 || stdout != created+"\n"+done+"\n" {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
}

func TestSecretsRequestStatusReadsAnEarlierRequest(t *testing.T) {
	srv := newEntryServer(t, entryReply{500, "unused"}, entryReply{200, entryDoc("pending", nil)})
	code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{Status: entryID})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(srv.requests) != 1 || srv.requests[0].Method != http.MethodGet || srv.requests[0].Path != "/api/deploy/secret-entry-requests/"+entryID+"/status" {
		t.Fatalf("requests: %+v", srv.requests)
	}
	for _, w := range []string{"Request " + entryID + " — STRIPE_API_KEY (deployment shop): pending", "Link:     https://console.dibbla.com/connector/secrets/" + entryID, "Expires:  2026-10-07 12:15 UTC (in 15 min)"} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	t.Logf("secrets request --status output:\n%s", stdout)

	// --status --wait --json on a request that has already ended: its
	// document once, and the exit code of how it ended.
	done := entryDoc("completed", nil)
	srv = newEntryServer(t, entryReply{500, "unused"}, entryReply{200, done})
	code, stdout, _, clock := runEntry(t, srv, entryRequestInput{Status: entryID, Wait: true, JSON: true})
	if code != 0 || stdout != done+"\n" || len(clock.slept) != 0 {
		t.Errorf("exit %d, slept %v, stdout:\n%s", code, clock.slept, stdout)
	}
}

func TestSecretsRequestStatusOfSomebodyElsesRequestIsAnAbsence(t *testing.T) {
	srv := newEntryServer(t, entryReply{500, "unused"})
	code, _, stderr, _ := runEntry(t, srv, entryRequestInput{Status: entryID})
	if code != 4 || !strings.Contains(stderr, "no request "+entryID+" for you in this organization") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

func TestSecretsRequestRefusesBadInputBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name string
		in   entryRequestInput
		want string
	}{
		{"no name", entryRequestInput{}, "name the secret"},
		{"bad name", entryRequestInput{Name: "stripe-key"}, "is not a secret name"},
		{"service without app", entryRequestInput{Name: "K", Service: "web"}, "--service requires --deployment"},
		{"bad alias", entryRequestInput{Name: "K", Deployment: "Shop!"}, "does not match"},
		{"long title", entryRequestInput{Name: "K", Title: strings.Repeat("x", 81)}, "--title must be one line"},
		{"title on two lines", entryRequestInput{Name: "K", Title: "a\nb"}, "--title must be one line"},
		{"long why", entryRequestInput{Name: "K", Why: strings.Repeat("x", 601)}, "--why must be plain text"},
		{"control character", entryRequestInput{Name: "K", Why: "a\x1b[31mb"}, "--why must be plain text"},
		{"status with a name", entryRequestInput{Name: "K", Status: entryID}, "--status reads a request made earlier"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)})
			code, _, stderr, _ := runEntry(t, srv, c.in)
			if code != 5 || !strings.Contains(stderr, c.want) {
				t.Errorf("exit %d: %s", code, stderr)
			}
			if len(srv.requests) != 0 {
				t.Errorf("a request was made: %+v", srv.requests)
			}
		})
	}
}

func TestSecretsRequestViewerIsToldWhichRoleItTakes(t *testing.T) {
	srv := newEntryServer(t, entryReply{403, `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"Your role in this organization cannot write secrets — owners, admins and developers can."}}`})
	code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_API_KEY", Deployment: "shop"})
	if code != 3 || !strings.Contains(stderr, "secrets request refused:") || !strings.Contains(stderr, "this command takes the role developer, admin or owner") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout on refusal: %s", stdout)
	}
}

func TestEnvPromoteRequestsAPromotionAndSaysTheOldValueIsSpent(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", map[string]any{"name": "STRIPE_SECRET_KEY", "promotes_env": true})})
	code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Deployment: "shop", Promote: true, Title: "Stripe secret key"})
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	body := srv.created(t)
	if body["promote_env"] != true || body["deployment_alias"] != "shop" || body["name"] != "STRIPE_SECRET_KEY" || body["title"] != "Stripe secret key" {
		t.Errorf("body: %v", body)
	}
	// Without --why the page still tells the person to rotate.
	if why, _ := body["explanation"].(string); !strings.Contains(why, "treat its value as exposed") || !strings.Contains(why, "new value") || len(why) > entryWhyMax {
		t.Errorf("default explanation: %q", why)
	}
	for _, w := range []string{
		"Promotion requested: STRIPE_SECRET_KEY (deployment shop) becomes a secret.",
		"treat it as exposed",
		"Rotate it where it was issued, then open this link and enter the NEW value; it never passes through this terminal:",
		"https://console.dibbla.com/connector/secrets/" + entryID,
		"Then:     the env var STRIPE_SECRET_KEY is removed and the app restarts with the secret. The page refuses the unchanged value.",
		"Check:    dibbla secrets request --status " + entryID,
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	t.Logf("env promote output:\n%s", stdout)

	// A --why of the agent's own is sent as given.
	srv = newEntryServer(t, entryReply{201, entryDoc("pending", map[string]any{"promotes_env": true})})
	runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Deployment: "shop", Promote: true, Why: "Roll the key first."})
	if got := srv.created(t)["explanation"]; got != "Roll the key first." {
		t.Errorf("explanation %q", got)
	}
}

func TestEnvPromoteWaitReportsTheSecret(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", map[string]any{"name": "STRIPE_SECRET_KEY", "promotes_env": true})},
		entryReply{200, entryDoc("completed", map[string]any{"name": "STRIPE_SECRET_KEY", "promotes_env": true})})
	code, stdout, _, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Deployment: "shop", Promote: true, Wait: true})
	if code != 0 || !strings.Contains(stdout, "STRIPE_SECRET_KEY (deployment shop) is a secret now: the env var is removed and the app restarts with the new value.") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
}

func TestEnvPromoteDefaultsToTheLinkedApp(t *testing.T) {
	gitEnv(t)
	work, _ := linkedRepo(t)
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", map[string]any{"promotes_env": true})})
	if code, _, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Promote: true, Dir: work}); code != 0 {
		t.Fatal(stderr)
	}
	if got := srv.created(t)["deployment_alias"]; got != "shop" {
		t.Errorf("deployment_alias %v, want the linked app", got)
	}

	srv = newEntryServer(t, entryReply{201, entryDoc("pending", nil)})
	code, _, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Promote: true, Dir: t.TempDir()})
	if code != 5 || !strings.Contains(stderr, "not linked") || len(srv.requests) != 0 {
		t.Errorf("unlinked folder: exit %d %s", code, stderr)
	}
}

func TestEnvPromoteRefusals(t *testing.T) {
	cases := []struct {
		name  string
		reply entryReply
		code  int
		want  []string
	}{
		{"no such env var", entryReply{404, `{"status":"error","error":{"code":"ENV_VAR_NOT_FOUND","message":"STRIPE_SECRET_KEY is not an env var of shop."}}`}, 4,
			[]string{"STRIPE_SECRET_KEY is not a plain env var of deployment shop, so there is nothing to promote", "dibbla secrets request"}},
		{"from the manifest", entryReply{409, `{"status":"error","error":{"code":"ENV_FROM_MANIFEST","message":"STRIPE_SECRET_KEY is set in dibbla.yaml; remove the line and redeploy."}}`}, 6,
			[]string{"comes from dibbla.yaml (environment:)", "remove the line and redeploy", "remove STRIPE_SECRET_KEY from dibbla.yaml, run 'dibbla secrets request STRIPE_SECRET_KEY -d shop' for a new value, then deploy"}},
		{"viewer", entryReply{403, `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"Your role in this organization cannot write secrets."}}`}, 3,
			[]string{"env promote refused:", "developer, admin or owner"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newEntryServer(t, c.reply)
			code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Deployment: "shop", Promote: true})
			if code != c.code {
				t.Errorf("exit %d, want %d: %s", code, c.code, stderr)
			}
			for _, w := range c.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if stdout != "" {
				t.Errorf("stdout on refusal: %s", stdout)
			}
			t.Logf("env promote, %s:\n%s", c.name, stderr)
		})
	}
}

// TestEnvPromoteOnAServerWithoutPromotion: a server from before DIB-1340
// ignores promote_env and makes a plain request. The link is not handed out,
// because entering a value there would leave the env var where it is.
func TestEnvPromoteOnAServerWithoutPromotion(t *testing.T) {
	srv := newEntryServer(t, entryReply{201, entryDoc("pending", nil)})
	code, stdout, stderr, _ := runEntry(t, srv, entryRequestInput{Name: "STRIPE_SECRET_KEY", Deployment: "shop", Promote: true, Wait: true})
	if code != 1 || !strings.Contains(stderr, "does not support promotion") || strings.Contains(stdout+stderr, "connector/secrets/") {
		t.Errorf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if srv.reads != 0 {
		t.Errorf("waited on a request that was not a promotion")
	}
}

func TestEntryCommandsAreRegistered(t *testing.T) {
	c, _, err := secretsCmd.Find([]string{"request"})
	if err != nil || c.Name() != "request" {
		t.Fatalf("secrets request: %v", err)
	}
	for _, f := range []string{"deployment", "service", "title", "why", "wait", "json", "status"} {
		if c.Flags().Lookup(f) == nil {
			t.Errorf("--%s missing on secrets request", f)
		}
	}
	p, _, err := envCmd.Find([]string{"promote"})
	if err != nil || p.Name() != "promote" {
		t.Fatalf("env promote: %v", err)
	}
	for _, f := range []string{"deployment", "service", "title", "why", "wait", "json"} {
		if p.Flags().Lookup(f) == nil {
			t.Errorf("--%s missing on env promote", f)
		}
	}
	for _, w := range []string{"never", "rotate", "dibbla.yaml", "env pull"} {
		if !strings.Contains(p.Long, w) {
			t.Errorf("env promote --help lacks %q", w)
		}
	}
}

// --- secrets set ------------------------------------------------------------------

const setValue = "sk_live_SYNTHETIC_do_not_echo"

type setRecorder struct {
	name, value, deployment, service string
	calls                            int
}

func (r *setRecorder) set(name, value, deployment, service string) (*secrets.SecretCreateResponse, error) {
	r.calls++
	r.name, r.value, r.deployment, r.service = name, value, deployment, service
	return &secrets.SecretCreateResponse{Status: "success", Message: "Secret created", Secret: secrets.SecretResponse{Name: name, DeploymentAlias: deployment, ServiceName: service}}, nil
}

func runSet(t *testing.T, in secretsSetInput) (code int, stdout, stderr string, rec *setRecorder) {
	t.Helper()
	rec = &setRecorder{}
	in.Set = rec.set
	if in.Stdin == nil {
		in.Stdin = strings.NewReader("")
	}
	if in.ReadHiddenLine == nil {
		in.ReadHiddenLine = func() ([]byte, error) { t.Fatal("prompted although stdin is not a terminal"); return nil, nil }
	}
	var out, errb bytes.Buffer
	code = runSecretsSetCore(&out, &errb, in)
	return code, out.String(), errb.String(), rec
}

func TestSecretsSetValueOnTheCommandLineWarns(t *testing.T) {
	code, stdout, stderr, rec := runSet(t, secretsSetInput{Args: []string{"STRIPE_API_KEY", setValue}, Deployment: "shop"})
	if code != 0 || rec.value != setValue || rec.deployment != "shop" {
		t.Fatalf("exit %d, set %+v: %s", code, rec, stderr)
	}
	for _, w := range []string{"in your shell history", "if an AI assistant ran this command, in its transcript", "dibbla secrets set STRIPE_API_KEY -d shop", "dibbla secrets request STRIPE_API_KEY -d shop"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr lacks %q:\n%s", w, stderr)
		}
	}
	if strings.Contains(stdout+stderr, setValue) {
		t.Error("the value reached the terminal")
	}
	if strings.Contains(stdout, "shell history") {
		t.Error("the warning went to stdout")
	}
	t.Logf("secrets set NAME VALUE, stderr:\n%s", stderr)
}

func TestSecretsSetAtATerminalAsksWithoutEcho(t *testing.T) {
	lines := []string{setValue, ""}
	read := 0
	code, stdout, stderr, rec := runSet(t, secretsSetInput{Args: []string{"STRIPE_API_KEY"}, StdinIsTerminal: true,
		ReadHiddenLine: func() ([]byte, error) { read++; return []byte(lines[read-1]), nil }})
	if code != 0 || rec.value != setValue || read != 2 {
		t.Fatalf("exit %d, %d reads, set %q: %s", code, read, rec.value, stderr)
	}
	if !strings.Contains(stderr, "Value for STRIPE_API_KEY (input hidden): paste it, then press Enter on an empty line: ") {
		t.Errorf("prompt: %q", stderr)
	}
	if strings.Contains(stdout+stderr, setValue) || strings.Contains(stderr, "shell history") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

// A pasted multi-line value is read to its end, so none of it is left in the
// terminal for the shell to run as commands.
func TestSecretsSetAtATerminalReadsAPastedKeyToTheEmptyLine(t *testing.T) {
	pem := []string{"-----BEGIN PRIVATE KEY-----", "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", "-----END PRIVATE KEY-----"}
	feed := append(append([]string{}, pem...), "", "echo should-not-be-read")
	read := 0
	code, stdout, stderr, rec := runSet(t, secretsSetInput{Args: []string{"TLS_KEY"}, Deployment: "shop", StdinIsTerminal: true,
		ReadHiddenLine: func() ([]byte, error) { read++; return []byte(feed[read-1] + "\r"), nil }})
	if code != 0 || rec.value != strings.Join(pem, "\n") || read != 4 {
		t.Fatalf("exit %d, %d reads, value %q: %s", code, read, rec.value, stderr)
	}
	if strings.Contains(stdout+stderr, "MIIEvQ") {
		t.Error("the key reached the terminal")
	}

	// End of input (Ctrl-D) ends it too.
	code, _, _, rec = runSet(t, secretsSetInput{Args: []string{"K"}, StdinIsTerminal: true,
		ReadHiddenLine: func() ([]byte, error) { return []byte(setValue), io.EOF }})
	if code != 0 || rec.value != setValue {
		t.Errorf("EOF: exit %d, value %q", code, rec.value)
	}
}

func TestSecretsSetReadsAPipeToItsEnd(t *testing.T) {
	code, stdout, stderr, rec := runSet(t, secretsSetInput{Args: []string{"TLS_KEY"}, Deployment: "shop", Service: "web",
		Stdin: strings.NewReader("line one\r\nline two\r\n")})
	if code != 0 || rec.value != "line one\nline two" || rec.service != "web" {
		t.Fatalf("exit %d, set %+v: %s", code, rec, stderr)
	}
	if strings.Contains(stdout+stderr, "line one") || strings.Contains(stderr, "Value for") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestSecretsSetEmptyValueSendsNothing(t *testing.T) {
	code, _, stderr, rec := runSet(t, secretsSetInput{Args: []string{"K"}, StdinIsTerminal: true,
		ReadHiddenLine: func() ([]byte, error) { return nil, nil }})
	if code != 1 || rec.calls != 0 || !strings.Contains(stderr, "secret value is required") {
		t.Errorf("exit %d, %d calls: %s", code, rec.calls, stderr)
	}
	code, _, stderr, rec = runSet(t, secretsSetInput{Args: []string{"K"}, StdinIsTerminal: true,
		ReadHiddenLine: func() ([]byte, error) { return nil, errors.New("not a terminal") }})
	if code != 1 || rec.calls != 0 || !strings.Contains(stderr, "Failed to read the value") {
		t.Errorf("read error: exit %d, %d calls: %s", code, rec.calls, stderr)
	}
}

func TestSecretsSetFailureNamesNoValue(t *testing.T) {
	var out, errb bytes.Buffer
	code := runSecretsSetCore(&out, &errb, secretsSetInput{Args: []string{"K", setValue}, Stdin: strings.NewReader(""),
		Set: func(string, string, string, string) (*secrets.SecretCreateResponse, error) {
			return nil, fmt.Errorf("ROLE_FORBIDDEN: no")
		}})
	if code != 1 || !strings.Contains(errb.String(), "Failed to set secret") || strings.Contains(out.String()+errb.String(), setValue) {
		t.Errorf("exit %d\n%s%s", code, out.String(), errb.String())
	}
}
