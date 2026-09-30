package notifycmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/notify"
)

const subID = "0b8e6a52-4a4c-4d0e-9a8e-2b6c1f0e9a11"

const subJSON = `{"id":"` + subID + `","scope":"personal","event_type":"application.check.failed","label":"A check fails","app":"shop","channel":"email","target":"dan@example.com","min_severity":"attention","digest":"immediate","enabled":true}`

type seen struct {
	method, path, query string
	body                map[string]any
}

func server(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, seen{r.Method, r.URL.Path, r.URL.RawQuery, body})
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestListShowsYoursAndForAdminsTheOrganizations(t *testing.T) {
	srv, got := server(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"subscriptions":[`+subJSON+`,{"id":"11111111-2222-4333-8444-555555555555","scope":"organization","event_type":"pipeline.run.*","label":"All pipeline alerts","channel":"email","target":"ops@example.com","min_severity":"attention","digest":"daily","enabled":true}],"can_manage_organization":true}`)
	})
	var out, errb bytes.Buffer
	if code := runList(&out, &errb, srv.URL, "tok", "shop", false); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if (*got)[0].path != "/api/notify/me/subscriptions" || (*got)[0].query != "app=shop" {
		t.Fatalf("request %+v", (*got)[0])
	}
	for _, want := range []string{"Yours (1)", "0b8e6a52", "A check fails (application.check.failed)", "shop · attention and up · email → dan@example.com", "The organization's (1)", "every app", "daily digest"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out.String())
		}
	}
}

func TestAddSendsTheRequestAndCanTestStraightAway(t *testing.T) {
	srv, got := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/notify/me/subscriptions":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"subscription":`+subJSON+`,"created":true}`)
		case "/api/notify/me/subscriptions/" + subID + "/test":
			fmt.Fprint(w, `{"event_id":"e1","subscription":`+subJSON+`,"delivery":{"id":"d1","subscription_id":"`+subID+`","channel":"email","target":"dan@example.com","status":"sent","attempts":1}}`)
		}
	})
	var out, errb bytes.Buffer
	code := runAdd(&out, &errb, srv.URL, "tok", notify.AddRequest{EventType: "application.check.failed", App: "shop", MinSeverity: "critical"}, true, false)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	body := (*got)[0].body
	if body["event_type"] != "application.check.failed" || body["app"] != "shop" || body["min_severity"] != "critical" {
		t.Fatalf("add body %v", body)
	}
	if _, orgWide := body["organization"]; orgWide {
		t.Fatalf("a personal add sent organization: %v", body)
	}
	if len(*got) != 2 || (*got)[1].method != http.MethodPost {
		t.Fatalf("--test did not send: %+v", *got)
	}
	if !strings.Contains(out.String(), "Subscribed: A check fails") || !strings.Contains(out.String(), "Test notification sent: email → dan@example.com") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestAPersonalAddWithoutAnAppIsRefusedLocally(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runAdd(&out, &errb, "http://unused.invalid", "tok", notify.AddRequest{EventType: "application.check.failed"}, false, false); code != 5 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "--app") || !strings.Contains(errb.String(), "--org-wide") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestTheServicesSentenceAndExitCodeReachTheUser(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"only owners and admins manage the organization's subscriptions; add a personal one instead"}`)
	})
	var out, errb bytes.Buffer
	code := runAdd(&out, &errb, srv.URL, "tok", notify.AddRequest{EventType: "pipeline.run.*", Organization: true, Target: "ops@example.com"}, false, false)
	if code != 3 || !strings.Contains(errb.String(), "only owners and admins") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestTestReportsAFailedDeliveryAndExitsNonZero(t *testing.T) {
	srv, got := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"subscriptions":[`+subJSON+`],"can_manage_organization":false}`)
			return
		}
		fmt.Fprint(w, `{"event_id":"e1","subscription":`+subJSON+`,"delivery":{"id":"d1","subscription_id":"`+subID+`","channel":"slack","status":"failed","attempts":1,"reason":"No Slack account is linked to you."}}`)
	})
	var out, errb bytes.Buffer
	// The eight characters the list prints are enough.
	if code := runTest(&out, &errb, srv.URL, "tok", "0b8e6a52", false); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if (*got)[1].path != "/api/notify/me/subscriptions/"+subID+"/test" {
		t.Fatalf("prefix not resolved: %+v", *got)
	}
	if !strings.Contains(errb.String(), "not delivered (failed)") || !strings.Contains(errb.String(), "No Slack account is linked") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestRemoveAsksUnlessToldAndRefusesWithoutATerminal(t *testing.T) {
	srv, got := server(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"deleted":true}`)
	})
	var out, errb bytes.Buffer
	noTTY := func(string) (bool, error) { return false, errors.New("not a terminal") }
	if code := runRemove(&out, &errb, srv.URL, "tok", subID, false, noTTY); code != 5 || len(*got) != 0 {
		t.Fatalf("without a terminal: exit %d, %d requests", code, len(*got))
	}
	if code := runRemove(&out, &errb, srv.URL, "tok", subID, true, noTTY); code != 0 {
		t.Fatalf("--yes: exit %d: %s", code, errb.String())
	}
	if (*got)[0].method != http.MethodDelete || (*got)[0].path != "/api/notify/me/subscriptions/"+subID {
		t.Fatalf("request %+v", (*got)[0])
	}
}

func TestHistoryShowsEachDeliveryAndItsReason(t *testing.T) {
	srv, got := server(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"scope":"personal","events":[{"id":"e1","type":"application.maintenance.finding.new","headline":"The maintenance agent found something to look at","app":"shop","severity":"attention","created_at":"2026-09-30T10:00:00Z","deliveries":[{"id":"d1","subscription_id":"`+subID+`","channel":"email","target":"dan@example.com","status":"failed","attempts":6,"reason":"The email provider did not accept the message; Dibbla stopped after 6 attempt(s)."}]}]}`)
	})
	var out, errb bytes.Buffer
	if code := runHistory(&out, &errb, srv.URL, "tok", "shop", 5, false); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if (*got)[0].path != "/api/notify/me/notification-history" || !strings.Contains((*got)[0].query, "app=shop") || !strings.Contains((*got)[0].query, "limit=5") {
		t.Fatalf("request %+v", (*got)[0])
	}
	for _, want := range []string{"[attention] The maintenance agent found something to look at — shop", "email → dan@example.com: failed — The email provider", "You see your own deliveries"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("history lacks %q:\n%s", want, out.String())
		}
	}
}

func TestEventsListsTheCatalog(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"groups":[{"key":"checks","label":"Application checks","family":{"event_type":"application.check.*","label":"All check alerts"},"events":[{"event_type":"application.check.failed","label":"A check fails","severity":"attention"}]}]}`)
	})
	var out, errb bytes.Buffer
	if code := runEvents(&out, &errb, srv.URL, "tok", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "application.check.*") || !strings.Contains(out.String(), "A check fails [attention]") {
		t.Fatalf("events:\n%s", out.String())
	}
}
