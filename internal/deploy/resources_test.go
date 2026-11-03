package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
)

func TestSeparateResourceUploadAndWarnings(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			for key, want := range map[string]string{"service": "web", "cpu_request": "100m", "cpu_limit": "1", "memory_request": "64Mi", "memory_limit": "2560Mi"} {
				if got := r.FormValue(key); got != want {
					t.Errorf("%s=%s want %s", key, got, want)
				}
			}
			result := `{"status":"success","deployment":{"alias":"burst","url":"https://burst.example"},"warnings":["low memory_request: eviction risk"]}`
			if streamed {
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintf(w, "{\"type\":\"result\",\"result\":%s}\n", result)
			} else {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, result)
			}
		}))
		var out bytes.Buffer
		printer := render.NewJSON(&out)
		result, err := upload(Options{APIURL: server.URL, Service: "web", CPURequest: "100m", CPULimit: "1", MemoryRequest: "64Mi", MemoryLimit: "2560Mi"}, nil, "burst", printer, func(r *render.DeployResult) { printer.OnEvent(render.DeployEvent{Type: "result", Result: r}) })
		if err != nil {
			t.Fatal(err)
		}
		if !streamed {
			if result == nil || len(result.Warnings) != 1 {
				t.Fatalf("lost warnings: %+v", result)
			}
		}
		if streamed {
			printer.OnDone()
			var doc map[string]any
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "eviction risk") {
				t.Fatalf("lost warning: %s", out.String())
			}
		}
		server.Close()
	}
}
