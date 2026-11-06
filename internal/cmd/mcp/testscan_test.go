package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityScanClientResumesThroughMCP(t *testing.T) {
	for _, crashAfterFinal := range []bool{false, true} {
		t.Run(fmt.Sprint(crashAfterFinal), func(t *testing.T) {
			raw := []byte("source archive bytes never in tool arguments")
			id := strings.Repeat("a", 64)
			offset := int64(0)
			starts, statuses, scans := 0, 0, 0
			dropped := false
			state := "created"
			var base string
			view := func() map[string]any {
				return map[string]any{"scan_id": id, "status": state, "offset": offset, "content_length": len(raw), "upload": uploadPlan{URL: base + "/platform/v1/security-scans/example-app/" + id + "/bytes", Method: "PUT", OffsetHeader: "Upload-Offset", MinChunkBytes: 1, MaxChunkBytes: 10}}
			}
			var written []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer opaque-token" {
					t.Error("missing caller OAuth")
				}
				b, _ := io.ReadAll(r.Body)
				if r.URL.Path == "/platform" {
					if bytes.Contains(b, raw) {
						t.Error("bytes crossed MCP arguments")
					}
					var call struct {
						Params struct {
							Name      string `json:"name"`
							Arguments struct {
								Action string `json:"action"`
								Scan   struct {
									Action string `json:"action"`
								} `json:"scan"`
							} `json:"arguments"`
						} `json:"params"`
					}
					_ = json.Unmarshal(b, &call)
					if call.Params.Name != "platform_files" || call.Params.Arguments.Action != "security_scan" {
						t.Error("wrong MCP flow")
					}
					switch call.Params.Arguments.Scan.Action {
					case "start":
						starts++
					case "status":
						statuses++
					default:
						t.Error("unexpected action")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": view()}})
					return
				}
				if offset == int64(len(raw)) && state == "created" {
					state = "completed"
					scans++
				} else if state == "created" {
					if r.Header.Get("Upload-Offset") != fmt.Sprint(offset) {
						w.WriteHeader(409)
						return
					}
					written = append(written, b...)
					offset += int64(len(b))
					if offset == int64(len(raw)) && !crashAfterFinal {
						state = "completed"
						scans++
					}
				}
				// Drop either the first committed chunk or the last pre-scan ack.
				if !dropped && (!crashAfterFinal || offset == int64(len(raw))) {
					dropped = true
					c, _, _ := w.(http.Hijacker).Hijack()
					_ = c.Close()
					return
				}
				_ = json.NewEncoder(w).Encode(view())
			}))
			defer srv.Close()
			base = srv.URL
			initial, err := scanControl(base+"/platform", "opaque-token", "example-app", map[string]any{"action": "start", "request_id": "1762460000-" + strings.Repeat("b", 32)}, map[string]any{"content_length": len(raw), "content_sha256": strings.Repeat("c", 64)})
			if err != nil {
				t.Fatal(err)
			}
			final, err := pushTestScan(writeTemp(t, raw), base+"/platform", "opaque-token", "example-app", int64(len(raw)), initial)
			if err != nil {
				t.Fatal(err)
			}
			if final.Status != "completed" || starts != 1 || statuses == 0 || scans != 1 || !bytes.Equal(written, raw) {
				t.Fatalf("status=%s starts=%d statuses=%d scans=%d bytes=%d", final.Status, starts, statuses, scans, len(written))
			}
		})
	}
}

func TestSecurityScanUploadRejectsCredentialRedirects(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, target := range []string{"https://evil.example/platform/v1/security-scans/app/" + id + "/bytes", "https://mcp.example/wrong", "https://user@mcp.example/platform/v1/security-scans/app/" + id + "/bytes"} {
		v := &transferView{ScanID: id, Upload: &uploadPlan{URL: target, MaxChunkBytes: chunkBytes}}
		if scanUploadPlan("https://mcp.example/platform", "app", v) == nil {
			t.Errorf("accepted %s", target)
		}
	}
	hits := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer sink.Close()
	var url string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"scan_id": id, "status": "failed", "error_code": "SCAN_INVALID_ARCHIVE", "offset": 0, "content_length": 1}}})
	}))
	defer srv.Close()
	url = srv.URL
	_, err := pushTestScan(writeTemp(t, []byte("x")), url+"/platform", "secret", "app", 1, &transferView{ScanID: id, Status: "created", ContentLength: 1, Upload: &uploadPlan{URL: url + "/platform/v1/security-scans/app/" + id + "/bytes", MaxChunkBytes: chunkBytes}})
	if err == nil || hits != 0 {
		t.Fatalf("redirect sent credential or hid failure: hits=%d err=%v", hits, err)
	}
}

func TestRegularUploadUsesCurrentMergedTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		p := b["params"].(map[string]any)
		a := p["arguments"].(map[string]any)
		if p["name"] != "platform_files" || a["action"] != "prepare_upload" {
			t.Error("obsolete upload tool")
		}
		fmt.Fprint(w, `{"result":{"structuredContent":{"transfer_id":"t","status":"issued"}}}`)
	}))
	defer srv.Close()
	if _, err := prepareUpload(srv.URL, "token", "source.tar.gz", "application/gzip", 10, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
}
