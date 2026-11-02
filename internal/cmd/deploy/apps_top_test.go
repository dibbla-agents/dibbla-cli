package deploy

import (
	"bytes"
	"strings"
	"testing"
)

// The four over-provisioned services in prod 2026-11-02 must be readable as
// such straight from the table: Dahl web 1000m reserved vs 11m used.
const usageDoc = `{"deployment_alias":"dahl","metrics_available":true,"warmup_seconds":300,"pods":[
{"service":"web","pod":"dahl-web-7c9f-abcde","state":"Running","restarts":0,"age_seconds":7200,"warming_up":false,
 "cpu":{"usage":11,"request":1000,"limit":1000,"percent_of_request":1.1,"percent_of_limit":1.1},
 "memory":{"usage":209715200,"request":1073741824,"limit":1073741824,"percent_of_request":19.5,"percent_of_limit":19.5}},
{"service":"worker","pod":"dahl-worker-0","state":"Running","restarts":1,"age_seconds":90,"warming_up":true,
 "cpu":{"usage":180,"request":100,"limit":0,"percent_of_request":180,"percent_of_limit":null},
 "memory":{"usage":104857600,"request":134217728,"limit":268435456,"percent_of_request":78.1,"percent_of_limit":39.1}}]}`

func TestAppsTopPrintsUsageNextToRequestAndLimit(t *testing.T) {
	srv, last, _ := newReleasesServer(t, 200, usageDoc)
	var out, errb bytes.Buffer
	if code := runAppsTopCore(&out, &errb, srv.URL, "tok", "dahl", false); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if last.Path != "/api/deploy/deployments/dahl/usage" || last.Method != "GET" {
		t.Errorf("request = %+v", last)
	}
	text := out.String()
	lines := strings.Split(text, "\n")
	var web, worker string
	for _, l := range lines {
		if strings.Contains(l, "abcde") {
			web = l
		}
		if strings.Contains(l, " 0 ") || strings.Contains(l, "worker") && strings.Contains(l, "180m") {
			worker = l
		}
	}
	for _, want := range []string{"web", "2h00m", "11m", "1000m", "1.1%", "200Mi", "1Gi", "20%"} {
		if !strings.Contains(web, want) {
			t.Errorf("web row lacks %q: %q", want, web)
		}
	}
	if strings.Contains(web, "START-UP") {
		t.Errorf("a steady pod is marked start-up: %q", web)
	}
	for _, want := range []string{"180m", "100m", "180%", "START-UP", "1 restart(s)"} {
		if !strings.Contains(worker, want) {
			t.Errorf("worker row lacks %q: %q", want, worker)
		}
	}
	if !strings.Contains(text, "not steady state") {
		t.Errorf("no start-up explanation:\n%s", text)
	}
}

func TestAppsTopWithoutMetricsStillShowsRequests(t *testing.T) {
	srv, _, _ := newReleasesServer(t, 200, `{"deployment_alias":"dahl","metrics_available":false,"metrics_error":"x","warmup_seconds":300,"pods":[
{"service":"web","pod":"dahl-web-1","state":"Running","age_seconds":7200,"cpu":{"usage":null,"request":250,"limit":250},"memory":{"usage":null,"request":536870912,"limit":536870912}}]}`)
	var out, errb bytes.Buffer
	if code := runAppsTopCore(&out, &errb, srv.URL, "tok", "dahl", false); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	text := out.String()
	for _, want := range []string{"usage unavailable", "250m", "512Mi", "?"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

func TestAppsTopJSONIsTheServerDocument(t *testing.T) {
	srv, _, _ := newReleasesServer(t, 200, usageDoc)
	var out, errb bytes.Buffer
	if code := runAppsTopCore(&out, &errb, srv.URL, "tok", "dahl", true); code != 0 {
		t.Fatal(code)
	}
	if strings.TrimSpace(out.String()) != strings.TrimSpace(usageDoc) {
		t.Errorf("--json changed the document")
	}
}

func TestAppsTopUnknownApp(t *testing.T) {
	srv, _, _ := newReleasesServer(t, 404, `{"error":{"code":"NOT_FOUND","message":"Deployment not found: ghost"}}`)
	var out, errb bytes.Buffer
	if code := runAppsTopCore(&out, &errb, srv.URL, "tok", "ghost", false); code == 0 {
		t.Fatal("exit 0 for an unknown app")
	}
	if !strings.Contains(errb.String(), "apps list") {
		t.Errorf("stderr = %s", errb.String())
	}
}
