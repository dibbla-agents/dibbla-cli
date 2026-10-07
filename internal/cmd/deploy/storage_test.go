package deploy

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/storage"
)

func personKey() *storage.BucketCredentials {
	return &storage.BucketCredentials{
		Endpoint:        "https://s3.example.com",
		Bucket:          "my-uploads",
		AccessKeyID:     "PERSONACCESSKEY",
		SecretAccessKey: "person/secret+key",
		SessionToken:    "eyJhbGciOi.session.token",
		ExpiresAt:       time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC),
	}
}

func issueReturning(c *storage.BucketCredentials, err error) func(string) (*storage.BucketCredentials, error) {
	return func(string) (*storage.BucketCredentials, error) { return c, err }
}

// -q prints the export lines and nothing else — that output is what
// `eval "$(dibbla storage credentials <name> -q)"` runs.
func TestStorageCredentials_QuietPrintsOnlyExportLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if code := storageCredentials(&stdout, &stderr, "my-uploads", true, now, issueReturning(personKey(), nil)); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	want := strings.Join([]string{
		"export AWS_ENDPOINT_URL='https://s3.example.com'",
		"export AWS_ACCESS_KEY_ID='PERSONACCESSKEY'",
		"export AWS_SECRET_ACCESS_KEY='person/secret+key'",
		"export AWS_SESSION_TOKEN='eyJhbGciOi.session.token'",
		"export DIBBLA_BUCKET='my-uploads'",
	}, "\n") + "\n"
	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("-q wrote to stderr: %q", stderr.String())
	}
}

// Without -q a person sees the same lines, when the key stops working, that it
// is not the app's key, and how to load it.
func TestStorageCredentials_ShowsExpiryAndHowToUseIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 10, 7, 12, 0, 30, 0, time.UTC)
	if code := storageCredentials(&stdout, &stderr, "my-uploads", false, now, issueReturning(personKey(), nil)); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Your own key for bucket 'my-uploads'",
		"valid until " + personKey().ExpiresAt.Local().Format("2006-01-02 15:04 MST"),
		"(in 60 min)",
		"  export AWS_SECRET_ACCESS_KEY='person/secret+key'",
		"not the app's key: the app's STORAGE_MY_UPLOADS_* secrets are untouched",
		`eval "$(dibbla storage credentials my-uploads -q)"`,
		"--s3-no-check-bucket",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// An error goes to stderr and stdout stays empty, so `eval "$(…)"` runs
// nothing.
func TestStorageCredentials_ErrorLeavesStdoutEmpty(t *testing.T) {
	for _, quiet := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		code := storageCredentials(&stdout, &stderr, "nope", quiet, time.Now(), issueReturning(nil, errors.New("BUCKET_NOT_FOUND: Bucket not found: nope")))
		if code != 1 {
			t.Errorf("quiet=%v: exit %d, want 1", quiet, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("quiet=%v: stdout = %q, want nothing", quiet, stdout.String())
		}
		if !strings.Contains(stderr.String(), "BUCKET_NOT_FOUND") {
			t.Errorf("quiet=%v: stderr = %q", quiet, stderr.String())
		}
	}
}

// A key without a session token (a server that hands out a static key) gets
// no AWS_SESSION_TOKEN line, rather than an empty one that tools would send.
func TestBucketKeyExports_NoSessionTokenNoLine(t *testing.T) {
	c := personKey()
	c.SessionToken = ""
	for _, l := range bucketKeyExports(c) {
		if strings.Contains(l, "AWS_SESSION_TOKEN") {
			t.Errorf("unexpected %q", l)
		}
	}
}

// What eval runs is data, whatever the server sends: a quote, a `$(…)` or a
// newline in a value lands in the variable verbatim and executes nothing.
func TestBucketKeyExports_EvalIsSafe(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	hostile := "it's $(echo pwned) `echo pwned`\nexport X=1; echo pwned"
	c := personKey()
	c.SecretAccessKey = hostile
	script := strings.Join(bucketKeyExports(c), "\n") + "\nprintf '%s' \"$AWS_SECRET_ACCESS_KEY\""
	out, err := exec.Command(sh, "-c", script).Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if string(out) != hostile {
		t.Errorf("eval produced %q, want the value verbatim %q", out, hostile)
	}
}

func TestExpiryPhrase(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if got := expiryPhrase(time.Time{}, now); got != "valid for about an hour" {
		t.Errorf("zero expiry: %q", got)
	}
	if got := expiryPhrase(now.Add(-time.Minute), now); !strings.HasPrefix(got, "already expired at ") {
		t.Errorf("past expiry: %q", got)
	}
	if got := expiryPhrase(now.Add(59*time.Minute+40*time.Second), now); !strings.HasSuffix(got, "(in 60 min)") {
		t.Errorf("rounding: %q", got)
	}
}
