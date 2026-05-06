package secrets

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recorderServer captures the last request observed by the test handler.
type recorderServer struct {
	srv     *httptest.Server
	method  string
	path    string
	query   string
	body    []byte
	status  int
	respond func(w http.ResponseWriter, r *http.Request)
}

func newRecorder(t *testing.T, status int, body any) *recorderServer {
	t.Helper()
	rs := &recorderServer{status: status}
	rs.respond = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.method = r.Method
		rs.path = r.URL.Path
		rs.query = r.URL.RawQuery
		rs.body, _ = io.ReadAll(r.Body)
		rs.respond(w, r)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func TestListSecrets_ForwardsServiceParam(t *testing.T) {
	rs := newRecorder(t, http.StatusOK, SecretsListResponse{
		Secrets: []SecretListItem{{Name: "API_KEY", DeploymentAlias: "myapp", ServiceName: "web"}},
		Total:   1,
	})
	out, err := ListSecrets(rs.srv.URL, "tok", "myapp", "web")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if rs.method != "GET" {
		t.Errorf("method: %q", rs.method)
	}
	if !strings.Contains(rs.query, "deployment=myapp") || !strings.Contains(rs.query, "service=web") {
		t.Errorf("query missing fields: %q", rs.query)
	}
	if len(out.Secrets) != 1 || out.Secrets[0].ServiceName != "web" {
		t.Errorf("response: %+v", out.Secrets)
	}
}

func TestListSecrets_OmitsServiceWhenEmpty(t *testing.T) {
	rs := newRecorder(t, http.StatusOK, SecretsListResponse{Total: 0})
	if _, err := ListSecrets(rs.srv.URL, "tok", "myapp", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(rs.query, "service=") {
		t.Errorf("query should not include service: %q", rs.query)
	}
}

func TestCreateSecret_ForwardsServiceField(t *testing.T) {
	rs := newRecorder(t, http.StatusCreated, SecretCreateResponse{
		Status: "success", Message: "Secret created successfully",
		Secret: SecretResponse{Name: "TOKEN", DeploymentAlias: "myapp", ServiceName: "web"},
	})
	out, err := CreateSecret(rs.srv.URL, "tok", "TOKEN", "v", "myapp", "web")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal(rs.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["service_name"] != "web" {
		t.Errorf("service_name not in body: %v", body)
	}
	if body["deployment_alias"] != "myapp" {
		t.Errorf("deployment_alias missing: %v", body)
	}
	if out.Secret.ServiceName != "web" {
		t.Errorf("response service: %q", out.Secret.ServiceName)
	}
}

func TestCreateSecret_OmitsServiceWhenEmpty(t *testing.T) {
	rs := newRecorder(t, http.StatusCreated, SecretCreateResponse{
		Status: "success", Secret: SecretResponse{Name: "TOKEN"},
	})
	if _, err := CreateSecret(rs.srv.URL, "tok", "TOKEN", "v", "myapp", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal(rs.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["service_name"]; ok {
		t.Errorf("service_name should be omitted when empty: %v", body)
	}
}

func TestGetSecret_ForwardsServiceParam(t *testing.T) {
	rs := newRecorder(t, http.StatusOK, SecretResponse{
		Name: "TOKEN", Value: "xxx", DeploymentAlias: "myapp", ServiceName: "web",
	})
	out, err := GetSecret(rs.srv.URL, "tok", "TOKEN", "myapp", "web")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(rs.query, "service=web") {
		t.Errorf("query: %q", rs.query)
	}
	if out.ServiceName != "web" {
		t.Errorf("service: %q", out.ServiceName)
	}
}

func TestDeleteSecret_ForwardsServiceParam(t *testing.T) {
	rs := newRecorder(t, http.StatusOK, DeleteResponse{Status: "success", Message: "ok"})
	if _, err := DeleteSecret(rs.srv.URL, "tok", "TOKEN", "myapp", "web"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(rs.query, "service=web") {
		t.Errorf("query: %q", rs.query)
	}
	if rs.method != "DELETE" {
		t.Errorf("method: %q", rs.method)
	}
}

func TestDeleteSecret_400Surfaced(t *testing.T) {
	rs := newRecorder(t, http.StatusBadRequest, ErrorResponse{
		Status: "error",
		Error:  APIError{Code: "VALIDATION_FAILED", Message: "service requires deployment"},
	})
	_, err := DeleteSecret(rs.srv.URL, "tok", "TOKEN", "", "web")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "VALIDATION_FAILED") {
		t.Errorf("missing code: %v", err)
	}
}
