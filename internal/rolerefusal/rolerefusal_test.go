package rolerefusal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const viewerRefusal = `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"your role (viewer) cannot use the console or the deploy API — owners, admins and developers can; ask an owner or admin to change your role","request_id":"req_1"}}`

const noRoleRefusal = `{"status":"error","error":{"code":"ROLE_FORBIDDEN","message":"you hold no role in this organization, so you cannot use the console or the deploy API — owners, admins and developers can; ask an owner or admin to add you","request_id":"req_2"}}`

// through serves one response through the transport and returns what a
// command would read.
func through(t *testing.T, status int, contentType, body string, authorized bool) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/deployments", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorized {
		req.Header.Set("Authorization", "Bearer ak_token")
	}
	client := &http.Client{Transport: &transport{base: http.DefaultTransport}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(got)
}

type envelope struct {
	Status string `json:"status"`
	Error  struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func TestAViewerReadsTheRoleTheRoleNeededAndWhoGivesIt(t *testing.T) {
	resp, body := through(t, http.StatusForbidden, "application/json", viewerRefusal, true)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got envelope
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("the rewritten body is not JSON: %v: %s", err, body)
	}
	for _, want := range []string{
		"Your role in this organization is viewer",
		"takes the role developer, admin or owner",
		"Ask an owner or admin of the organization",
		"Org settings, Members",
		"'dibbla login'",
	} {
		if !strings.Contains(got.Error.Message, want) {
			t.Errorf("the message lacks %q: %s", want, got.Error.Message)
		}
	}
	// A command is somebody at a terminal: the words for the console's
	// reader are not theirs.
	for _, not := range []string{"deploy API", "cannot use the console"} {
		if strings.Contains(got.Error.Message, not) {
			t.Errorf("the message still says %q: %s", not, got.Error.Message)
		}
	}
	if got.Error.Code != Code || got.Error.RequestID != "req_1" || got.Status != "error" {
		t.Errorf("the other fields changed: %+v", got)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length %d for a body of %d bytes", resp.ContentLength, len(body))
	}
}

func TestSomebodyWithNoRoleIsNotGivenOne(t *testing.T) {
	_, body := through(t, http.StatusForbidden, "application/json", noRoleRefusal, true)
	var got envelope
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error.Message, "You hold no role in this organization") ||
		!strings.Contains(got.Error.Message, "Ask an owner or admin") {
		t.Errorf("message: %s", got.Error.Message)
	}
	if strings.Contains(got.Error.Message, "role in this organization is") {
		t.Errorf("a role was invented: %s", got.Error.Message)
	}
}

func TestEverythingElseIsLeftAsItWas(t *testing.T) {
	big := `{"error":{"code":"ROLE_FORBIDDEN","message":"` + strings.Repeat("x", maxBody) + `"}}`
	for name, c := range map[string]struct {
		status      int
		contentType string
		body        string
		authorized  bool
	}{
		"another 403":                {403, "application/json", `{"status":"error","error":{"code":"FORBIDDEN","message":"Not a member of this organization"}}`, true},
		"the code on another status": {409, "application/json", viewerRefusal, true},
		"a success":                  {200, "application/json", `{"deployments":[],"note":"ROLE_FORBIDDEN"}`, true},
		"not JSON":                   {403, "text/html", "<html>ROLE_FORBIDDEN</html>", true},
		"JSON that is not ours":      {403, "application/json", `["ROLE_FORBIDDEN"]`, true},
		"no credential of ours":      {403, "application/json", viewerRefusal, false},
		"larger than an envelope":    {403, "application/json", big, true},
		"an empty body":              {403, "application/json", "", true},
	} {
		_, got := through(t, c.status, c.contentType, c.body, c.authorized)
		if got != c.body {
			t.Errorf("%s: the body changed:\n got %.200s\nwant %.200s", name, got, c.body)
		}
	}
}

func TestABodyRewrittenOnceIsNotRewrittenAgain(t *testing.T) {
	once, ok := rewrite([]byte(viewerRefusal))
	if !ok {
		t.Fatal("not rewritten")
	}
	twice, ok := rewrite(once)
	if !ok {
		t.Fatal("the rewritten body is no longer a refusal")
	}
	if string(once) != string(twice) {
		t.Errorf("rewritten twice:\n%s\n%s", once, twice)
	}
	if !strings.Contains(string(twice), "role in this organization is viewer") {
		t.Errorf("the role was lost: %s", twice)
	}
}

func TestExplain(t *testing.T) {
	if msg, ok := Explain([]byte(viewerRefusal)); !ok || !strings.Contains(msg, "is viewer") {
		t.Errorf("Explain = %q, %v", msg, ok)
	}
	for _, body := range []string{"", "not json", `{"error":"ROLE_FORBIDDEN"}`, `{"error":{"code":"FORBIDDEN","message":"x"}}`} {
		if msg, ok := Explain([]byte(body)); ok {
			t.Errorf("Explain(%q) = %q", body, msg)
		}
	}
}
