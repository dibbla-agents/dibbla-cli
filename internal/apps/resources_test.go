package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateAppSeparateResourceRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method=%s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for k, v := range map[string]string{"service": "web", "cpu_request": "100m", "cpu_limit": "1", "memory_request": "1Gi", "memory_limit": "2560Mi"} {
			if body[k] != v {
				t.Errorf("%s=%v want %s", k, body[k], v)
			}
		}
		if _, ok := body["cpu"]; ok {
			t.Error("shorthand was sent as well")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"burst","warnings":["memory eviction risk"]}`)
	}))
	defer server.Close()
	dep, err := UpdateApp(server.URL, "test", "burst", UpdateDeploymentRequest{Service: "web", CPURequest: "100m", CPULimit: "1", MemoryRequest: "1Gi", MemoryLimit: "2560Mi"})
	if err != nil || len(dep.Warnings) != 1 {
		t.Fatalf("response=%+v err=%v", dep, err)
	}
}
