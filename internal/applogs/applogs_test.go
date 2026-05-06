package applogs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStream_ForwardsServiceQueryParam(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body, err := Stream(context.Background(), srv.URL, "tok", "myapp", Options{
		Since:   1 * time.Minute,
		Follow:  true,
		Service: "worker",
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer body.Close()
	_, _ = io.Copy(io.Discard, body)
	if !strings.Contains(sawQuery, "service=worker") {
		t.Errorf("query missing service=worker: %q", sawQuery)
	}
	if !strings.Contains(sawQuery, "follow=true") {
		t.Errorf("query missing follow: %q", sawQuery)
	}
}

func TestStream_OmitsServiceWhenEmpty(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body, err := Stream(context.Background(), srv.URL, "tok", "myapp", Options{Since: time.Minute})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer body.Close()
	_, _ = io.Copy(io.Discard, body)
	if strings.Contains(sawQuery, "service=") {
		t.Errorf("query should not include service: %q", sawQuery)
	}
}

func TestStreamPodService_HappyPath(t *testing.T) {
	var sawPath, sawQuery, sawAuth, sawAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawQuery = r.URL.RawQuery
		sawAuth = r.Header.Get("Authorization")
		sawAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[pod-1] hello\n[pod-2] world\n")
	}))
	defer srv.Close()

	body, err := StreamPodService(context.Background(), srv.URL, "tok", "myapp", "worker", PodStreamOptions{
		Tail: 50, Follow: true,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer body.Close()
	out, _ := io.ReadAll(body)
	if string(out) != "[pod-1] hello\n[pod-2] world\n" {
		t.Errorf("body: %q", string(out))
	}
	if sawPath != "/api/deploy/deployments/myapp/services/worker/logs" {
		t.Errorf("path: %q", sawPath)
	}
	if !strings.Contains(sawQuery, "tail=50") || !strings.Contains(sawQuery, "follow=true") {
		t.Errorf("query: %q", sawQuery)
	}
	if sawAuth != "Bearer tok" {
		t.Errorf("auth: %q", sawAuth)
	}
	if sawAccept != "text/plain" {
		t.Errorf("accept: %q", sawAccept)
	}
}

func TestStreamPodService_RejectsBadServiceLocally(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	_, err := StreamPodService(context.Background(), srv.URL, "tok", "myapp", "Web", PodStreamOptions{Follow: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if hits != 0 {
		t.Errorf("server should NOT be hit: %d", hits)
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestStreamPodService_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":"error","error":{"code":"NOT_FOUND","message":"no pods"}}`)
	}))
	defer srv.Close()

	_, err := StreamPodService(context.Background(), srv.URL, "tok", "myapp", "worker", PodStreamOptions{Follow: false})
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected *HTTPError, got %T (%v)", err, err)
	}
	if httpErr.Status != 404 {
		t.Errorf("status: %d", httpErr.Status)
	}
}
