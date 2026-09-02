package mcp

// `dibbla mcp platform --upload <file>` proves the agent-redeemed upload path
// end to end (DIB-670): prepare an intent over MCP, then move the bytes from
// this process straight to the platform, resuming from the committed offset if
// the link drops.
//
// This is what an MCP client with a filesystem does. The command exists so the
// path has a client that can be run and read — and so a client author can see
// the shape without reverse-engineering it: the tool answer's `upload` field
// is a plan the process acts on, the bytes never enter a tool call, and the
// only credential involved is the platform access token the client already
// holds.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/platformcontract"
)

// errChunkInterrupted is a chunk whose outcome the client does not know: the
// request left, and no answer came back. It is not a failure — it is the state
// the resume protocol exists for.
var errChunkInterrupted = errors.New("the connection dropped mid-chunk")

// uploadCapability is the contract row this command drives. The scope it needs
// is read from that row rather than written here — the same rule the rest of
// this CLI follows, and the reason a scope rename cannot leave the advice in
// these errors pointing at a scope that no longer exists.
const uploadCapability = "platform.files.put"

// uploadScopeRequest is the --login scope a grant needs to move bytes. An
// error that names the problem without naming the fix is half an error, so
// this is what the failures below print.
func uploadScopeRequest() string {
	scopes := []string{}
	for _, c := range platformcontract.Capabilities() {
		if c.ID == uploadCapability {
			scopes = append(scopes, c.Scopes...)
		}
	}
	if len(scopes) == 0 {
		// The vendored contract is verified at build time, so this is
		// unreachable; degrade to the identity scope rather than print an
		// empty --scope the user would have to guess at.
		return identityScope()
	}
	return strings.Join(append([]string{identityScope()}, scopes...), " ")
}

// identityScope is the one scope every grant carries, taken from the
// capability that defines it.
func identityScope() string {
	for _, c := range platformcontract.Capabilities() {
		if c.ID == "platform.identity.whoami" && len(c.Scopes) > 0 {
			return c.Scopes[0]
		}
	}
	return ""
}

// chunkBytes is what one PUT carries. Small enough that a dropped connection
// costs little, large enough that a 50 MB archive is a handful of requests.
const chunkBytes = 8 << 20

// uploadPlan is the `upload` field of an upload intent. It is the whole client
// contract: where to PUT, which header carries the offset, and how large a
// chunk may be. It holds no credential — the caller presents its own token.
type uploadPlan struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	OffsetHeader string `json:"offset_header"`
	// MinChunkBytes is the smallest chunk that is not the last one — the
	// broker's multipart part size. Sending less is refused, so this is not
	// advice, it is the shape a chunked upload has to have.
	MinChunkBytes int64  `json:"min_chunk_bytes"`
	MaxChunkBytes int64  `json:"max_chunk_bytes"`
	ExpiresAt     string `json:"expires_at"`
}

type transferView struct {
	TransferID   string      `json:"transfer_id"`
	Status       string      `json:"status"`
	Offset       int64       `json:"offset"`
	SourceFileID string      `json:"source_file_id"`
	FailureCode  string      `json:"failure_code"`
	FallbackURL  string      `json:"fallback_url"`
	Upload       *uploadPlan `json:"upload"`
}

func runPlatformUpload(w io.Writer, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory — pack it into an archive first", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty; there is nothing to upload", path)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	fmt.Fprintf(w, "File:      %s  (%d bytes, sha256 %s…)\n", name, info.Size(), sum[:12])

	endpoint, token, err := platformSession(w)
	if err != nil {
		return err
	}

	view, err := prepareUpload(endpoint, token, name, contentTypeFor(name), info.Size(), sum)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Intent:    %s %s  (transfer %s)\n", platform.Icon("✅", "[OK]"), view.Status, view.TransferID)
	if view.Upload == nil {
		return fmt.Errorf("this grant prepared the intent but got no upload plan, so the bytes would have to go through a browser at %s.\nTo upload from here instead, get a grant that may write files:\n  dibbla mcp platform --login --scope %q\nA write scope is never granted by default — tick it on the consent page", view.FallbackURL, uploadScopeRequest())
	}

	final, err := pushBytes(w, path, token, view)
	if err != nil {
		return err
	}
	if final.Status != "completed" || final.SourceFileID == "" {
		return fmt.Errorf("the upload ended as %s (%s) and produced no file", final.Status, final.FailureCode)
	}
	fmt.Fprintf(w, "Uploaded:  %s %d bytes in one transfer\n", platform.Icon("✅", "[OK]"), final.Offset)
	fmt.Fprintf(w, "File:      %s\n", final.SourceFileID)
	return nil
}

// platformSession resolves the endpoint and a presentable access token,
// refreshing and re-storing the grant when the stored one has expired.
func platformSession(w io.Writer) (string, string, error) {
	endpoint, source, err := platformToolset.endpoint()
	if err != nil {
		return "", "", err
	}
	fmt.Fprintf(w, "Endpoint:  %s  (%s)\n", endpoint, source.Source)
	ctxName := grantContextName()
	g, store, err := loadGrant(ctxName)
	if err != nil {
		return "", "", fmt.Errorf("%w — run `dibbla mcp platform --login` to replace it", err)
	}
	if g == nil {
		return "", "", fmt.Errorf("no platform grant for context %q: run `dibbla mcp platform --login`", ctxName)
	}
	status, err := tokenStatus(g)
	if err != nil {
		return "", "", err
	}
	if status.refreshed {
		if _, err := saveGrant(ctxName, g, store); err != nil {
			return "", "", err
		}
	}
	return endpoint, g.AccessToken, nil
}

func prepareUpload(endpoint, token, name, contentType string, size int64, sum string) (*transferView, error) {
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	call := map[string]any{"name": "platform_file_upload_prepare", "arguments": map[string]any{
		"name": name, "content_type": contentType, "content_length": size,
		"content_sha256": sum, "idempotency_key": newIdempotencyKey(),
	}}
	client := &http.Client{Timeout: 30 * time.Second}
	if err := rpcCall(client, platformToolset, endpoint, token, "tools/call", call, &resp); err != nil {
		return nil, platformProbeAdvice(err)
	}
	text := ""
	if len(resp.Result.Content) > 0 {
		text = resp.Result.Content[0].Text
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("platform_file_upload_prepare failed: %s", resp.Error.Message)
	}
	if resp.Result.IsError {
		return nil, fmt.Errorf("platform_file_upload_prepare refused the intent: %s", strings.TrimSpace(text))
	}
	var view transferView
	if err := json.Unmarshal(resp.Result.StructuredContent, &view); err != nil {
		return nil, fmt.Errorf("the upload intent could not be read: %w", err)
	}
	if view.TransferID == "" {
		return nil, fmt.Errorf("the upload intent carried no transfer id: %s", strings.TrimSpace(text))
	}
	return &view, nil
}

// pushBytes streams the file to the plan's URL, one bounded chunk at a time.
// A chunk refused with the committed offset is not an error: the server is
// telling this client where the bytes actually stand, and the loop continues
// from there under the same transfer id.
func pushBytes(w io.Writer, path, token string, view *transferView) (*transferView, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	size := int64(chunkBytes)
	if max := view.Upload.MaxChunkBytes; max > 0 && max < size {
		size = max
	}
	// A non-final chunk below the broker's minimum is refused outright, so the
	// minimum wins over the preferred size. Only the last chunk may be smaller,
	// and it is short simply because the file ends.
	if min := view.Upload.MinChunkBytes; min > size {
		if view.Upload.MaxChunkBytes > 0 && min > view.Upload.MaxChunkBytes {
			return nil, fmt.Errorf("the platform asks for chunks of at least %d bytes but accepts at most %d — this upload cannot be chunked", min, view.Upload.MaxChunkBytes)
		}
		size = min
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	buf := make([]byte, size)
	offset, resumes, attempts := view.Offset, 0, 0
	current := view
	for current.Status != "completed" {
		n, readErr := f.ReadAt(buf, offset)
		if n == 0 {
			if readErr != nil && readErr != io.EOF {
				return nil, readErr
			}
			return nil, fmt.Errorf("the server wants bytes from offset %d, which is past the end of %s", offset, path)
		}
		next, committed, err := putChunk(client, token, view.Upload, offset, buf[:n])
		if errors.Is(err, errChunkInterrupted) {
			// The connection died with the outcome unknown: the server may
			// have committed this chunk or none of it. Resending the same
			// offset settles it — an already-committed chunk comes back as an
			// offset conflict naming the real offset, which the loop below
			// resumes from. Nothing is written twice either way.
			attempts++
			if attempts > 5 {
				return nil, fmt.Errorf("the upload kept losing its connection at offset %d: %w", offset, err)
			}
			fmt.Fprintf(w, "Retry:     %s connection lost at offset %d, resending\n", platform.Icon("↻", "[~]"), offset)
			time.Sleep(time.Duration(attempts) * 200 * time.Millisecond)
			continue
		}
		if err != nil {
			return nil, err
		}
		attempts = 0
		if next == nil {
			// Offset conflict: resume where the server says the bytes are.
			if committed == offset {
				return nil, fmt.Errorf("the server refused offset %d but reports the same offset; giving up rather than looping", offset)
			}
			resumes++
			if resumes > 8 {
				return nil, fmt.Errorf("the committed offset kept moving; giving up after %d resumes", resumes)
			}
			fmt.Fprintf(w, "Resume:    %s committed offset is %d\n", platform.Icon("↩︎", "[<]"), committed)
			offset = committed
			continue
		}
		current = next
		offset = next.Offset
		fmt.Fprintf(w, "Uploading: %d bytes committed\r", offset)
	}
	fmt.Fprintln(w)
	return current, nil
}

// putChunk sends one chunk. It returns the new view, or (nil, committedOffset)
// when the server refused this offset and named the real one.
func putChunk(client *http.Client, token string, plan *uploadPlan, offset int64, chunk []byte) (*transferView, int64, error) {
	method := plan.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequest(method, plan.URL, strings.NewReader(string(chunk)))
	if err != nil {
		return nil, 0, err
	}
	offsetHeader := plan.OffsetHeader
	if offsetHeader == "" {
		offsetHeader = "Upload-Offset"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(offsetHeader, strconv.FormatInt(offset, 10))
	req.ContentLength = int64(len(chunk))

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", errChunkInterrupted, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusConflict {
		committed, cerr := strconv.ParseInt(strings.TrimSpace(resp.Header.Get(offsetHeader)), 10, 64)
		if cerr != nil {
			return nil, 0, fmt.Errorf("the server refused offset %d without naming the committed one: %s", offset, uploadErrorText(raw))
		}
		return nil, committed, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, 0, uploadAdvice(resp.StatusCode, raw)
	}
	var view transferView
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, 0, fmt.Errorf("the server's answer could not be read: %w", err)
	}
	return &view, view.Offset, nil
}

// uploadAdvice turns the byte route's refusal into the sentence a user acts on.
func uploadAdvice(status int, raw []byte) error {
	text := uploadErrorText(raw)
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("the platform rejected the token mid-upload (HTTP 401): %s — run `dibbla mcp platform --login` and retry", text)
	case http.StatusForbidden:
		return fmt.Errorf("this grant does not cover file uploads (HTTP 403): %s\nRun `dibbla mcp platform --login --scope %q` and tick the write scope on the consent page", text, uploadScopeRequest())
	case http.StatusNotFound:
		return fmt.Errorf("the transfer is gone, or the grant behind it was revoked mid-upload (HTTP 404): %s", text)
	case http.StatusGone:
		return fmt.Errorf("the transfer expired or was already finished (HTTP 410): %s — prepare a new upload", text)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("the platform verified the bytes against what was declared and refused them (HTTP 422): %s — this upload produced no file", text)
	default:
		return fmt.Errorf("the upload failed (HTTP %d): %s", status, text)
	}
}

func uploadErrorText(raw []byte) string {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		if env.Error.Message != "" {
			return env.Error.Code + " — " + env.Error.Message
		}
		return env.Error.Code
	}
	return strings.TrimSpace(string(raw))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// contentTypeFor declares what the file is. The declaration is metadata the
// platform stores; the bytes are verified against the length and sha256, not
// against this.
func contentTypeFor(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return "application/octet-stream"
}

// newIdempotencyKey names this attempt. It is deliberately fresh per run: a
// key derived from the file would make a second run inherit the terminal state
// of the first, so a corrected file could not be uploaded until the earlier
// intent expired.
func newIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "cli-upload-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "cli-upload-" + hex.EncodeToString(b[:])
}
