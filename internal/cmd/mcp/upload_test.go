package mcp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/platformcontract"
)

// uploadServer is the platform's byte route: it accepts a chunk only at the
// committed offset, names the committed one when it refuses, and completes at
// the declared length.
type uploadServer struct {
	t *testing.T
	// dropAt makes the first chunk that starts at this offset land without
	// the client learning it, the way a connection that dies after the server
	// committed does. The client then has to resume from the 409.
	dropAt   int64
	dropped  bool
	total    int64
	written  []byte
	statuses []int
	tokens   []string
}

func (s *uploadServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.tokens = append(s.tokens, r.Header.Get("Authorization"))
		offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		if err != nil {
			http.Error(w, `{"error":{"code":"VALIDATION_FAILED"}}`, http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if offset != int64(len(s.written)) {
			w.Header().Set("Upload-Offset", strconv.Itoa(len(s.written)))
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"OFFSET_MISMATCH","message":"the committed offset moved"}}`)
			s.statuses = append(s.statuses, http.StatusConflict)
			return
		}
		s.written = append(s.written, raw...)
		if !s.dropped && s.dropAt >= 0 && offset == s.dropAt {
			// Committed, then the answer is lost.
			s.dropped = true
			hj, ok := w.(http.Hijacker)
			if !ok {
				s.t.Fatal("test server cannot simulate a dropped answer")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		status := "active"
		fileID := ""
		if int64(len(s.written)) >= s.total {
			status, fileID = "completed", "file_abcdefghijklmnop"
		}
		w.Header().Set("Upload-Offset", strconv.Itoa(len(s.written)))
		w.Header().Set("Content-Type", "application/json")
		s.statuses = append(s.statuses, http.StatusOK)
		fmt.Fprintf(w, `{"transfer_id":"12345678-1234-4123-8123-123456789abc","status":%q,"offset":%d,"source_file_id":%q}`,
			status, len(s.written), fileID)
	})
}

func writeTemp(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "build.tar.gz")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The whole client half: chunk the file, send each chunk at the committed
// offset with the caller's own token, and finish with the opaque file id.
func TestPushBytesUploadsInChunks(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 3*chunkBytes+11)
	srv := &uploadServer{t: t, dropAt: -1, total: int64(len(content))}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	view := &transferView{TransferID: "t", Status: "issued",
		Upload: &uploadPlan{URL: server.URL, Method: http.MethodPut, OffsetHeader: "Upload-Offset"}}
	final, err := pushBytes(io.Discard, writeTemp(t, content), "tok", view)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if final.Status != "completed" || final.SourceFileID != "file_abcdefghijklmnop" {
		t.Fatalf("final = %+v", final)
	}
	if !bytes.Equal(srv.written, content) {
		t.Fatalf("server received %d bytes, sent %d", len(srv.written), len(content))
	}
	if len(srv.statuses) != 4 {
		t.Fatalf("%d chunks for %d bytes", len(srv.statuses), len(content))
	}
	for _, auth := range srv.tokens {
		if auth != "Bearer tok" {
			t.Fatalf("chunk carried %q — the client's own token is the only credential", auth)
		}
	}
}

// An interrupted transfer resumes at the broker's committed offset, under the
// same transfer id, and writes no duplicate.
func TestPushBytesResumesFromCommittedOffset(t *testing.T) {
	content := bytes.Repeat([]byte("y"), 2*chunkBytes+5)
	srv := &uploadServer{t: t, dropAt: 0, total: int64(len(content))}
	server := httptest.NewServer(srv.handler())
	defer server.Close()

	var log strings.Builder
	view := &transferView{TransferID: "t", Status: "issued",
		Upload: &uploadPlan{URL: server.URL, Method: http.MethodPut, OffsetHeader: "Upload-Offset"}}
	final, err := pushBytes(&log, writeTemp(t, content), "tok", view)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if final.Status != "completed" {
		t.Fatalf("final = %+v", final)
	}
	if !bytes.Equal(srv.written, content) {
		t.Fatalf("resume duplicated or lost bytes: got %d, want %d", len(srv.written), len(content))
	}
	out := log.String()
	if !strings.Contains(out, "connection lost") || !strings.Contains(out, "committed offset is") {
		t.Fatalf("the resume was silent: %q", out)
	}
}

// A refusal is reported as the thing the user does next, and the platform's
// verification failure is never softened into a retry.
func TestUploadAdviceNamesTheFix(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "--login",
		http.StatusForbidden:           "does not cover file uploads",
		http.StatusNotFound:            "revoked",
		http.StatusGone:                "prepare a new upload",
		http.StatusUnprocessableEntity: "produced no file",
		http.StatusInternalServerError: "HTTP 500",
	}
	for status, want := range cases {
		err := uploadAdvice(status, []byte(`{"error":{"code":"X","message":"m"}}`))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%d: %v does not mention %q", status, err, want)
		}
	}
}

func TestUploadPreflightRefusesUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	if err := runPlatformUpload(io.Discard, dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory: %v", err)
	}
	empty := filepath.Join(dir, "empty.tgz")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runPlatformUpload(io.Discard, empty); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty: %v", err)
	}
	if err := runPlatformUpload(io.Discard, filepath.Join(dir, "absent")); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("absent: %v", err)
	}
}

func TestFileDigestAndDeclaredType(t *testing.T) {
	sum, err := fileSHA256(writeTemp(t, []byte("hello world!")))
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello world!")
	if sum != "7509e5bda0c762d2bac7f90d758b5b2263fa01ccbc542ab5e3df163be08e6ca9" {
		t.Fatalf("sha256 = %s", sum)
	}
	if got := contentTypeFor("build.tar.gz"); got == "" || strings.Contains(got, ";") {
		t.Fatalf("content type = %q", got)
	}
	if got := contentTypeFor("archive.unknown-ext"); got != "application/octet-stream" {
		t.Fatalf("unknown extension = %q", got)
	}
}

func TestIdempotencyKeysAreFreshPerRun(t *testing.T) {
	a, b := newIdempotencyKey(), newIdempotencyKey()
	if a == b || !strings.HasPrefix(a, "cli-upload-") || len(a) > 128 {
		t.Fatalf("keys %q %q", a, b)
	}
}

// The broker refuses a non-final chunk below its multipart minimum. A client
// that ignores the minimum and picks its own preferred size gets a refusal it
// cannot act on, so the plan's minimum wins over the preference.
func TestPushBytesHonoursTheMinimumChunk(t *testing.T) {
	// Larger than the client's own preferred chunk, so the assertion is that
	// the plan's minimum wins rather than that the two happen to agree.
	minChunk := int64(chunkBytes) + (2 << 20)
	content := bytes.Repeat([]byte("z"), int(minChunk)+1024)
	var sizes []int
	srv := &uploadServer{t: t, dropAt: -1, total: int64(len(content))}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(r.ContentLength)
		final := len(srv.written)+n >= len(content)
		if !final && int64(n) < minChunk {
			t.Errorf("non-final chunk of %d bytes is below the %d minimum", n, minChunk)
		}
		sizes = append(sizes, n)
		srv.handler().ServeHTTP(w, r)
	}))
	defer server.Close()

	// A plan whose minimum is larger than this client's preferred chunk.
	view := &transferView{TransferID: "t", Status: "issued", Upload: &uploadPlan{
		URL: server.URL, Method: http.MethodPut, OffsetHeader: "Upload-Offset",
		MinChunkBytes: minChunk, MaxChunkBytes: 64 << 20,
	}}
	final, err := pushBytes(io.Discard, writeTemp(t, content), "tok", view)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if final.Status != "completed" || !bytes.Equal(srv.written, content) {
		t.Fatalf("final = %+v, %d bytes", final, len(srv.written))
	}
	if len(sizes) != 2 || int64(sizes[0]) != minChunk {
		t.Fatalf("chunk sizes %v; want the minimum first, then the remainder", sizes)
	}
}

// A plan whose minimum exceeds its maximum cannot be satisfied; say so instead
// of sending a chunk that is certain to be refused.
func TestPushBytesRefusesAnImpossiblePlan(t *testing.T) {
	view := &transferView{TransferID: "t", Status: "issued", Upload: &uploadPlan{
		URL: "http://127.0.0.1:1", Method: http.MethodPut, OffsetHeader: "Upload-Offset",
		MinChunkBytes: 10 << 20, MaxChunkBytes: 1 << 20,
	}}
	_, err := pushBytes(io.Discard, writeTemp(t, bytes.Repeat([]byte("q"), 32)), "tok", view)
	if err == nil || !strings.Contains(err.Error(), "cannot be chunked") {
		t.Fatalf("impossible plan: %v", err)
	}
}

// The scope this command tells a user to ask for is read from the vendored
// contract, not written in the source. A scope rename would otherwise leave
// the advice pointing at something the issuer no longer knows.
func TestUploadScopeRequestComesFromTheContract(t *testing.T) {
	got := uploadScopeRequest()
	if got == "" || !strings.Contains(got, ":files:") || !strings.Contains(got, ":identity:") {
		t.Fatalf("scope request = %q", got)
	}
	// Every scope named must exist in the registry.
	for _, name := range strings.Fields(got) {
		if _, ok := platformcontract.LookupScope(name); !ok {
			t.Fatalf("%q is not a scope the contract defines", name)
		}
	}
	// And it must be exactly what the capability this command drives requires.
	var want []string
	for _, c := range platformcontract.Capabilities() {
		if c.ID == uploadCapability {
			want = c.Scopes
		}
	}
	if len(want) == 0 {
		t.Fatal("the contract has no row for the capability this command drives")
	}
	for _, name := range want {
		if !strings.Contains(got, name) {
			t.Fatalf("scope request %q omits %q, which %s requires", got, name, uploadCapability)
		}
	}
}
