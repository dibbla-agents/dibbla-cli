package deploy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeployCancelCallsTheCancelEndpoint(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
		code   int
	}{
		{"cancelled here", 200, `{"alias":"myapp","cancelled":true,"deployment_id":"dep_1"}`, "Cancelled deploy dep_1 of myapp", 0},
		{"on the other process", 202, `{"alias":"myapp","cancelled":true,"deployment_id":"dep_2"}`, "Cancel requested for deploy dep_2", 0},
		{"nothing running", 200, `{"alias":"myapp","cancelled":false}`, "No deploy in progress for myapp", 0},
		{"a delete", 409, `{"error":{"code":"DEPLOY_IN_PROGRESS","message":"this app is being deleted"}}`, "being deleted", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod = r.URL.Path, r.Method
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			var out, errOut bytes.Buffer
			code := runDeployCancel(&out, &errOut, srv.URL, "tok", "myapp")
			if gotMethod != http.MethodPost || gotPath != "/api/deploy/deployments/myapp/cancel" {
				t.Errorf("%s %s", gotMethod, gotPath)
			}
			if code != tc.code {
				t.Errorf("exit = %d, want %d (stderr=%s)", code, tc.code, errOut.String())
			}
			if !strings.Contains(out.String()+errOut.String(), tc.want) {
				t.Errorf("output %q lacks %q", out.String()+errOut.String(), tc.want)
			}
		})
	}
}
