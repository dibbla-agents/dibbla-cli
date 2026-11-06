package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var scanRequestPattern = regexp.MustCompile(`^[0-9]{10}-[a-f0-9]{32}$`)
var scanAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var scanIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func runPlatformTestScan(w io.Writer, path, alias, requestID string) error {
	if !scanAliasPattern.MatchString(alias) {
		return fmt.Errorf("--testscan needs a valid application --alias")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 50<<20 {
		return fmt.Errorf("Security scan needs a nonempty source archive of at most 50 MiB")
	}
	if requestID == "" {
		var b [16]byte
		if _, err = rand.Read(b[:]); err != nil {
			return err
		}
		requestID = fmt.Sprintf("%d-%s", time.Now().Unix(), hex.EncodeToString(b[:]))
	}
	if !scanRequestPattern.MatchString(requestID) {
		return fmt.Errorf("request_id must be Unix seconds, a hyphen and 32 lowercase hex characters")
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return err
	}
	// Printed before network I/O, so a lost start response can be retried safely.
	fmt.Fprintf(w, "request_id=%s\n", requestID)
	endpoint, token, err := platformSession(w)
	if err != nil {
		return err
	}
	view, err := scanControl(endpoint, token, alias, map[string]any{"action": "start", "request_id": requestID}, map[string]any{"content_length": info.Size(), "content_sha256": sum})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "scan_id=%s\n", view.ScanID)
	final, err := pushTestScan(path, endpoint, token, alias, info.Size(), view)
	if err != nil {
		return fmt.Errorf("Security scan %s: %w; retain request_id=%s to resume, or end scan_id via MCP", view.ScanID, err, requestID)
	}
	fmt.Fprintf(w, "Security scan scan_id=%s status=%s. Read all result pages, compare, then end via platform_files action=security_scan.\n", final.ScanID, final.Status)
	return nil
}

func scanControl(endpoint, token, alias string, scan, metadata map[string]any) (*transferView, error) {
	args := map[string]any{"action": "security_scan", "alias": alias, "scan": scan}
	for k, v := range metadata {
		args[k] = v
	}
	var response struct {
		Result struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := rpcCall(&http.Client{Timeout: 30 * time.Second}, platformToolset, endpoint, token, "tools/call", map[string]any{"name": "platform_files", "arguments": args}, &response); err != nil {
		return nil, platformProbeAdvice(err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("Security scan MCP call refused: %s", response.Error.Message)
	}
	if response.Result.IsError {
		text := "Security scan refused"
		if len(response.Result.Content) > 0 {
			text = response.Result.Content[0].Text
		}
		return nil, fmt.Errorf("%s; the CLI's own OAuth grant needs platform:apps:read and platform:files:write", text)
	}
	var view transferView
	if err := json.Unmarshal(response.Result.StructuredContent, &view); err != nil {
		return nil, err
	}
	if !scanIDPattern.MatchString(view.ScanID) || view.Status == "" {
		return nil, fmt.Errorf("incomplete Security scan response")
	}
	return &view, nil
}

func scanUploadPlan(endpoint, alias string, view *transferView) error {
	if view.Upload == nil {
		return fmt.Errorf("Security scan returned no upload plan")
	}
	origin, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	target, err := url.Parse(view.Upload.URL)
	if err != nil {
		return err
	}
	want := "/platform/v1/security-scans/" + alias + "/" + view.ScanID + "/bytes"
	if origin.Host == "" || target.Scheme != origin.Scheme || target.Host != origin.Host || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Path != want || (view.Upload.Method != "" && view.Upload.Method != http.MethodPut) || (view.Upload.OffsetHeader != "" && !strings.EqualFold(view.Upload.OffsetHeader, "Upload-Offset")) {
		return fmt.Errorf("Security scan upload plan is outside this authenticated connection")
	}
	if view.Upload.MaxChunkBytes < 1 || view.Upload.MaxChunkBytes > chunkBytes || view.Upload.MinChunkBytes > view.Upload.MaxChunkBytes {
		return fmt.Errorf("invalid Security scan chunk bounds")
	}
	return nil
}

func pushTestScan(path, endpoint, token, alias string, length int64, view *transferView) (*transferView, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	id := view.ScanID
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(4 * time.Minute)
	stalls := 0
	for time.Now().Before(deadline) {
		if view.ScanID != id || view.Offset < 0 || view.Offset > length || view.ContentLength != length {
			return nil, fmt.Errorf("Security scan returned inconsistent upload metadata")
		}
		switch view.Status {
		case "completed":
			return view, nil
		case "created":
			if err := scanUploadPlan(endpoint, alias, view); err != nil {
				return nil, err
			}
			size := view.Upload.MaxChunkBytes
			offset := view.Offset
			// Recover a crash after final offset commit but before scanning began.
			if offset == length {
				offset = length - size
				if offset < 0 {
					offset = 0
				}
			}
			if size > length-offset {
				size = length - offset
			}
			b := make([]byte, size)
			if n, err := f.ReadAt(b, offset); n != len(b) || (err != nil && err != io.EOF) {
				return nil, fmt.Errorf("source archive changed or could not be read")
			}
			next, _, uploadErr := putChunk(client, token, view.Upload, offset, b)
			if uploadErr == nil && next != nil {
				if next.Offset > view.Offset || next.Status != "created" {
					stalls = 0
				} else {
					stalls++
				}
				view = next
				if stalls > 5 {
					return nil, fmt.Errorf("Security scan upload made no progress")
				}
				continue
			}
			// Lost acknowledgement, stale offset, or busy admission: read the same
			// run through MCP; never prepare a second run or trust an offset alone.
			stalls++
			if stalls > 5 {
				return nil, fmt.Errorf("Security scan upload retries exhausted: %v", uploadErr)
			}
		case "uploading", "scanning":
		default:
			return nil, fmt.Errorf("Security scan ended with status=%s error_code=%s", view.Status, view.ErrorCode)
		}
		time.Sleep(200 * time.Millisecond)
		view, err = scanControl(endpoint, token, alias, map[string]any{"action": "status", "scan_id": id}, nil)
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("Security scan is still pending; retry the same request identity")
}
