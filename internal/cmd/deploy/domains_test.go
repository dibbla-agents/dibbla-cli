package deploy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
)

const domainRowPending = `{
	"id":"dom_1","deployment_alias":"myapp","hostname":"www.exempel.se",
	"status":"pending","ssl_status":"pending_validation","active":false,
	"errors":["The custom hostname must resolve to the CNAME target"],
	"dns":{"type":"CNAME","name":"www.exempel.se","target":"cname.dibbla.com","record":"www.exempel.se CNAME cname.dibbla.com"},
	"is_apex":false,"created_at":"2026-09-17T10:00:00Z","updated_at":"2026-09-17T10:00:00Z"}`

const domainRowActive = `{
	"id":"dom_2","deployment_alias":"myapp","hostname":"app.exempel.se",
	"status":"active","ssl_status":"active","active":true,
	"dns":{"type":"CNAME","name":"app.exempel.se","target":"cname.dibbla.com","record":"app.exempel.se CNAME cname.dibbla.com"},
	"is_apex":false,"created_at":"2026-09-17T10:00:00Z","updated_at":"2026-09-17T10:00:00Z"}`

const domainRowApex = `{
	"id":"dom_3","deployment_alias":"myapp","hostname":"exempel.se",
	"status":"pending","ssl_status":"initializing","active":false,
	"dns":{"type":"CNAME","name":"exempel.se","target":"cname.dibbla.com","record":"exempel.se CNAME cname.dibbla.com"},
	"is_apex":true,"apex_advice":"Point the www hostname at the platform with a CNAME. Most registrars cannot put a CNAME on the bare domain (apex); set up an HTTP redirect from the apex to www there instead.",
	"created_at":"2026-09-17T10:00:00Z","updated_at":"2026-09-17T10:00:00Z"}`

const domainRowParked = `{
	"id":"dom_4","deployment_alias":"myapp","hostname":"shop.exempel.se",
	"status":"disconnected","ssl_status":"active","active":false,"parked":true,
	"disconnected_at":"2026-09-17T12:00:00Z",
	"dns":{"type":"CNAME","name":"shop.exempel.se","target":"cname.dibbla.com","record":"shop.exempel.se CNAME cname.dibbla.com"},
	"is_apex":false,"created_at":"2026-09-17T10:00:00Z","updated_at":"2026-09-17T12:00:00Z"}`

func domainsServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDomainsAdd_PrintsCNAMEAndApexAdvice(t *testing.T) {
	var gotBody string
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/deploy/deployments/myapp/domains" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(domainRowPending))
	})

	var stdout, stderr bytes.Buffer
	if code := runDomainsAddCore(&stdout, &stderr, srv.URL, "tok", "myapp", "WWW.Exempel.se.", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if gotBody != `{"hostname":"www.exempel.se"}` {
		t.Errorf("hostname should be normalized before sending, got body %s", gotBody)
	}
	out := stdout.String()
	for _, want := range []string{
		"Type:   CNAME",
		"Host:   www",
		"Target: cname.dibbla.com",
		"www.exempel.se CNAME cname.dibbla.com",
		"cannot put a CNAME on the bare domain",
		"redirect from it to www.exempel.se",
		"waiting for DNS",
		"The custom hostname must resolve",
		"dibbla domains verify myapp www.exempel.se",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestDomainsAdd_ApexUsesServerAdvice(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(domainRowApex))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsAddCore(&stdout, &stderr, srv.URL, "tok", "myapp", "exempel.se", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Host:   @", "bare domain (apex)", "HTTP redirect from the apex to www"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestDomainsAdd_JSONIsVerbatim(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // the owner's own row handed back
		_, _ = w.Write([]byte(`{"hostname":"www.exempel.se","future_field":1}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsAddCore(&stdout, &stderr, srv.URL, "tok", "myapp", "www.exempel.se", true); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, stdout.String())
	}
	if _, ok := got["future_field"]; !ok {
		t.Errorf("verbatim output should keep unknown server fields: %s", stdout.String())
	}
}

func TestDomainsAdd_TakenIsExit6WithoutOwner(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"DOMAIN_TAKEN","message":"www.exempel.se is already registered"}}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsAddCore(&stdout, &stderr, srv.URL, "tok", "myapp", "www.exempel.se", false); code != 6 {
		t.Fatalf("exit %d, want 6 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "already connected to another app") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestDomainsAdd_LocalRefusals(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runDomainsAddCore(&stdout, &stderr, "http://127.0.0.1:1", "tok", "myapp", "not a host", false); code != 5 {
		t.Errorf("bad hostname: exit %d, want 5", code)
	}
	if code := runDomainsAddCore(&stdout, &stderr, "http://127.0.0.1:1", "tok", "BAD", "www.exempel.se", false); code != 5 {
		t.Errorf("bad alias: exit %d, want 5", code)
	}
	if code := runDomainsAddCore(&stdout, &stderr, "http://127.0.0.1:1", "tok", "myapp", "http://www.exempel.se", false); code != 5 {
		t.Errorf("url instead of hostname: exit %d, want 5", code)
	}
}

func TestDomainsList_ShowsStatusPerDomain(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/deploy/deployments/myapp/domains" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"domains":[` + domainRowPending + `,` + domainRowActive + `],"cname_target":"cname.dibbla.com","apex_advice":"…"}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsListCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"www.exempel.se", "pending_validation", "app.exempel.se", "active", "yes", "no", "waiting for DNS", "CNAME target: cname.dibbla.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestDomainsList_Empty(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"domains":[],"cname_target":"cname.dibbla.com","apex_advice":"…"}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsListCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No domains connected") || !strings.Contains(stdout.String(), "dibbla domains add myapp") {
		t.Errorf("output: %s", stdout.String())
	}
}

func TestDomainsList_AppNotFoundIsExit4(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"NOT_FOUND","message":"Deployment not found: ghost"}}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsListCore(&stdout, &stderr, srv.URL, "tok", "ghost", false); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
}

func TestDomainsVerify_ActiveAndPending(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"domains":[` + domainRowPending + `,` + domainRowActive + `],"cname_target":"cname.dibbla.com","apex_advice":"…"}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsVerifyCore(&stdout, &stderr, srv.URL, "tok", "myapp", "app.exempel.se", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "active — serving") || !strings.Contains(stdout.String(), "https://app.exempel.se") {
		t.Errorf("active output: %s", stdout.String())
	}

	stdout.Reset()
	if code := runDomainsVerifyCore(&stdout, &stderr, srv.URL, "tok", "myapp", "www.exempel.se", false); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"waiting for DNS", "Hostname status: pending", "Certificate:     pending_validation", "must resolve to the CNAME target", "Target: cname.dibbla.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in pending output:\n%s", want, out)
		}
	}

	stdout.Reset()
	if code := runDomainsVerifyCore(&stdout, &stderr, srv.URL, "tok", "myapp", "nope.exempel.se", false); code != 4 {
		t.Fatalf("unknown hostname: exit %d, want 4 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not connected to 'myapp'") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestDomainsVerify_JSONIsTheOneRow(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"domains":[` + domainRowPending + `,` + domainRowActive + `],"cname_target":"cname.dibbla.com","apex_advice":"…"}`))
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsVerifyCore(&stdout, &stderr, srv.URL, "tok", "myapp", "app.exempel.se", true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["hostname"] != "app.exempel.se" || got["active"] != true {
		t.Errorf("json: %s", stdout.String())
	}
}

func TestDomainsRemove(t *testing.T) {
	var gotPath string
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		if strings.HasSuffix(r.URL.Path, "/ghost.exempel.se") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"DOMAIN_NOT_FOUND","message":"domain ghost.exempel.se is not registered on myapp"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsRemoveCore(&stdout, &stderr, srv.URL, "tok", "myapp", "www.exempel.se"); code != 0 {
		t.Fatalf("exit %d (stderr=%q)", code, stderr.String())
	}
	if gotPath != "DELETE /api/deploy/deployments/myapp/domains/www.exempel.se" {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(stdout.String(), "disconnected from 'myapp'") {
		t.Errorf("output: %s", stdout.String())
	}
	if code := runDomainsRemoveCore(&stdout, &stderr, srv.URL, "tok", "myapp", "ghost.exempel.se"); code != 4 {
		t.Fatalf("exit %d, want 4 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ghost.exempel.se is not connected to 'myapp'") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

// DIB-861: a parked domain lists as "disconnected" with the way back; verify
// says so without DNS instructions; add on a parked hostname (the server
// answers 200 active) prints no CNAME block; remove explains the parking.
func TestDomainsParked_ListVerifyAddRemove(t *testing.T) {
	srv := domainsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"domains":[` + domainRowParked + `],"cname_target":"cname.dibbla.com","apex_advice":"…"}`))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Replace(domainRowActive, "app.exempel.se", "shop.exempel.se", -1)))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	var stdout, stderr bytes.Buffer
	if code := runDomainsListCore(&stdout, &stderr, srv.URL, "tok", "myapp", false); code != 0 {
		t.Fatalf("list exit %d (stderr=%q)", code, stderr.String())
	}
	for _, want := range []string{"shop.exempel.se", "disconnected", "not connected", "domains add"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("list missing %q:\n%s", want, stdout.String())
		}
	}
	stdout.Reset()
	if code := runDomainsVerifyCore(&stdout, &stderr, srv.URL, "tok", "myapp", "shop.exempel.se", false); code != 0 {
		t.Fatalf("verify exit %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "disconnected") || !strings.Contains(out, "dibbla domains add myapp shop.exempel.se") || strings.Contains(out, "Create this DNS record") {
		t.Errorf("verify output:\n%s", out)
	}
	stdout.Reset()
	if code := runDomainsAddCore(&stdout, &stderr, srv.URL, "tok", "myapp", "shop.exempel.se", false); code != 0 {
		t.Fatalf("add exit %d (stderr=%q)", code, stderr.String())
	}
	out = stdout.String()
	if !strings.Contains(out, "active — serving") || !strings.Contains(out, "https://shop.exempel.se") || strings.Contains(out, "Type:") {
		t.Errorf("reconnect output must not carry DNS instructions:\n%s", out)
	}
	stdout.Reset()
	if code := runDomainsRemoveCore(&stdout, &stderr, srv.URL, "tok", "myapp", "shop.exempel.se"); code != 0 {
		t.Fatalf("remove exit %d (stderr=%q)", code, stderr.String())
	}
	for _, want := range []string{"not connected", "30 days", "dibbla domains add myapp shop.exempel.se"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("remove missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDNSHostLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		apex bool
		want string
	}{
		{"www.exempel.se", false, "www"},
		{"exempel.se", true, "@"},
		{"app.sub.exempel.se", false, "app"},
		{"exempel.co.uk", true, "@"},
	} {
		d := &apps.Domain{IsApex: tc.apex, DNS: apps.DNSInstruction{Name: tc.name}}
		if got := dnsHostLabel(d); got != tc.want {
			t.Errorf("dnsHostLabel(%q)=%q want %q", tc.name, got, tc.want)
		}
	}
}
