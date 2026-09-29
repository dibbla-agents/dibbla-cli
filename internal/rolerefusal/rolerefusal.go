// Package rolerefusal turns the platform's 403 ROLE_FORBIDDEN into a sentence
// somebody at a terminal can act on.
//
// Dibbla's API asks for the organization role developer or above on
// everything the CLI works with: apps, logs, secrets, databases, storage. A
// new member of an organization is a viewer until an owner or admin says
// otherwise, so the refusal is what a colleague who was just invited meets on
// their first `dibbla apps list`. What they need to read is which role they
// hold, which one the command takes, and who can give it to them.
//
// The CLI reads API errors in some fifteen places, each with its own error
// type and its own rendering. Teaching every one of them this sentence would
// miss the sixteenth. So, like the organization header (internal/orgctx), it
// is done in one place: the transport rewrites the refusal's message on its
// way in, and every command prints it through whatever rendering it has.
package rolerefusal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Code is the platform's error code for a role that does not reach the route.
const Code = "ROLE_FORBIDDEN"

// maxBody bounds what is read looking for the refusal. An error envelope is a
// few hundred bytes; anything larger is not one.
const maxBody = 64 << 10

// heldRole finds the role in the server's own sentence, "your role (viewer)
// cannot …". The envelope has no field for it.
var heldRole = regexp.MustCompile(`your role \(([a-z_-]{1,32})\)`)

// Message is the CLI's sentence for a refusal whose server message is given.
func Message(serverMessage string) string {
	held := "You hold no role in this organization that reaches it"
	if m := heldRole.FindStringSubmatch(serverMessage); m != nil {
		held = "Your role in this organization is " + m[1]
	}
	return held + ", and this command takes the role developer, admin or owner. " +
		"Ask an owner or admin of the organization to change your role — they do it in the console under Org settings, Members. " +
		"If the command is still refused after that, run 'dibbla login' again."
}

// Explain reports the CLI's sentence when body is a ROLE_FORBIDDEN envelope,
// for a caller that holds a raw error body instead of a parsed one.
func Explain(body []byte) (string, bool) {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Error.Code != Code {
		return "", false
	}
	if strings.HasSuffix(envelope.Error.Message, rewritten) {
		return envelope.Error.Message, true
	}
	return Message(envelope.Error.Message), true
}

// rewritten ends every message this package wrote, so a body that passes
// through twice is left alone.
const rewritten = "run 'dibbla login' again."

type transport struct {
	base http.RoundTripper
}

// Install wraps http.DefaultTransport. Call it once at startup.
func Install() {
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	http.DefaultTransport = &transport{base: base}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode != http.StatusForbidden || resp.Body == nil {
		return resp, err
	}
	// Only the CLI's own authenticated calls: a 403 from anywhere else is
	// not the platform's and is not read.
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		return resp, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return resp, err
	}

	head, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	rest := resp.Body
	restore := func(body []byte) {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), rest), rest}
	}
	if readErr != nil || len(head) > maxBody {
		restore(head)
		return resp, nil
	}

	out, ok := rewrite(head)
	if !ok {
		restore(head)
		return resp, nil
	}
	restore(out)
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return resp, nil
}

// rewrite replaces error.message in a ROLE_FORBIDDEN envelope and leaves
// every other field as the server sent it.
func rewrite(body []byte) ([]byte, bool) {
	message, ok := Explain(body)
	if !ok {
		return nil, false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope["error"], &fields) != nil {
		return nil, false
	}
	quoted, err := json.Marshal(message)
	if err != nil {
		return nil, false
	}
	fields["message"] = quoted
	if envelope["error"], err = json.Marshal(fields); err != nil {
		return nil, false
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, false
	}
	return out, true
}

// Error is the refusal as an error, for a command that returns one.
func Error(message string) error {
	return fmt.Errorf("%s: %s", Code, message)
}
