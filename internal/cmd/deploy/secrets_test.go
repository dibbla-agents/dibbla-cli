package deploy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireServiceWithDeployment_NoServicePasses(t *testing.T) {
	var buf bytes.Buffer
	if !requireServiceWithDeployment(&buf, "", "") {
		t.Errorf("global scope should be allowed")
	}
	if buf.Len() != 0 {
		t.Errorf("no error expected, got: %q", buf.String())
	}
}

func TestRequireServiceWithDeployment_ServiceWithDeploymentPasses(t *testing.T) {
	var buf bytes.Buffer
	if !requireServiceWithDeployment(&buf, "myapp", "web") {
		t.Errorf("service+deployment should be allowed")
	}
	if buf.Len() != 0 {
		t.Errorf("no error expected, got: %q", buf.String())
	}
}

func TestRequireServiceWithDeployment_ServiceWithoutDeploymentRejected(t *testing.T) {
	var buf bytes.Buffer
	if requireServiceWithDeployment(&buf, "", "web") {
		t.Errorf("service-only should be rejected")
	}
	if !strings.Contains(buf.String(), "--service requires --deployment") {
		t.Errorf("expected guard message, got: %q", buf.String())
	}
}

func TestScopeLabel(t *testing.T) {
	cases := []struct {
		dep, svc, want string
	}{
		{"", "", "global"},
		{"myapp", "", "deployment myapp"},
		{"myapp", "web", "deployment myapp, service web"},
	}
	for _, c := range cases {
		if got := scopeLabel(c.dep, c.svc); got != c.want {
			t.Errorf("scopeLabel(%q,%q) = %q, want %q", c.dep, c.svc, got, c.want)
		}
	}
}

func TestSecretsCmd_HasServiceFlags(t *testing.T) {
	for _, sub := range []string{"list", "set", "get", "delete"} {
		c, _, err := secretsCmd.Find([]string{sub})
		if err != nil {
			t.Fatalf("find %s: %v", sub, err)
		}
		if c.Flags().Lookup("service") == nil {
			t.Errorf("--service flag missing on secrets %s", sub)
		}
	}
}

func TestSecretNameRe(t *testing.T) {
	valid := []string{"A", "API_KEY", "a1", "DATABASE_URL", "x_9"}
	invalid := []string{"", "1ABC", "a-b", "a.b", "WITH SPACE", "lower-case-dash"}
	for _, v := range valid {
		if !secretNameRe.MatchString(v) {
			t.Errorf("expected %q to be a valid secret name", v)
		}
	}
	for _, v := range invalid {
		if secretNameRe.MatchString(v) {
			t.Errorf("expected %q to be an invalid secret name", v)
		}
	}
}

func TestSecretsImportCmd_Registered(t *testing.T) {
	c, _, err := secretsCmd.Find([]string{"import"})
	if err != nil {
		t.Fatalf("find import: %v", err)
	}
	for _, f := range []string{"deployment", "service", "env", "dry-run"} {
		if c.Flags().Lookup(f) == nil {
			t.Errorf("--%s flag missing on secrets import", f)
		}
	}
}

// writeEnvFile writes a temporary .env file and returns its path.
func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return p
}

// recordingSetter captures the (name, value) pairs an import would send,
// standing in for the network so tests can assert exactly what was written.
type recordingSetter struct {
	calls  []string // "NAME=value", in call order
	failOn string   // when non-empty, return an error at this key
}

func (r *recordingSetter) set(name, value string) error {
	if r.failOn != "" && name == r.failOn {
		return errors.New("boom: API rejected the write")
	}
	r.calls = append(r.calls, name+"="+value)
	return nil
}

// Criterion 5: one invalid key rejects the whole file — nothing is sent.
//
// The keys here (leading digit, dot, space, leading underscore) are ones
// godotenv itself parses happily but the server's name rule rejects — i.e.
// exactly the cases where our up-front validation is what stops a half-applied
// import. Keys containing "-" are not usable here: godotenv fails them at parse
// time with "unexpected character", which is the separate missing/malformed-file
// path covered by TestSecretsImportCore_MalformedFileSendsNothing.
func TestSecretsImportCore_InvalidKeyIsAllOrNothing(t *testing.T) {
	file := writeEnvFile(t, "GOOD_KEY=ok\n1BAD=nope\na.b=nope\nWITH SPACE=nope\n_LEAD=nope\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "shop", "", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit for an invalid key, got %d", code)
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing should have been sent, but got %d call(s): %v", len(rec.calls), rec.calls)
	}
	for _, want := range []string{"1BAD", "a.b", "WITH SPACE", "_LEAD", "Nothing was imported"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("expected stderr to mention %q, got: %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "GOOD_KEY") {
		t.Errorf("valid keys should not be listed as invalid, got: %q", stderr.String())
	}
}

// A file godotenv cannot parse fails before anything is sent, same as a missing
// one. A "-" in a variable name is the common real-world trigger.
func TestSecretsImportCore_MalformedFileSendsNothing(t *testing.T) {
	file := writeEnvFile(t, "GOOD_KEY=ok\nalso-bad=nope\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "shop", "", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit for a malformed file")
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing should have been sent, got: %v", rec.calls)
	}
	if !strings.Contains(stderr.String(), "loading env file") {
		t.Errorf("expected a file-loading error naming the file, got: %q", stderr.String())
	}
}

// Criterion 6: values never appear in output — names and counts only.
func TestSecretsImportCore_NeverPrintsValues(t *testing.T) {
	const secret1, secret2 = "sk-live-SUPERSECRET", "hunter2-also-secret"
	file := writeEnvFile(t, "API_KEY="+secret1+"\nDATABASE_URL="+secret2+"\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, []string{"EXTRA=flag-value-secret"},
		"shop", "", false, rec.set)

	if code != 0 {
		t.Fatalf("expected success, got exit %d (stderr: %q)", code, stderr.String())
	}
	out := stdout.String() + stderr.String()
	for _, leaked := range []string{secret1, secret2, "flag-value-secret"} {
		if strings.Contains(out, leaked) {
			t.Errorf("value %q leaked into output: %q", leaked, out)
		}
	}
	// The names, however, must be there.
	for _, name := range []string{"API_KEY", "DATABASE_URL", "EXTRA"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected key name %q in output, got: %q", name, out)
		}
	}
	// And the values must still have reached the setter, in sorted order.
	want := "API_KEY=" + secret1 + " DATABASE_URL=" + secret2 + " EXTRA=flag-value-secret"
	if got := strings.Join(rec.calls, " "); got != want {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// Criterion 6 again, on the failure path: a mid-loop error must not echo values.
func TestSecretsImportCore_FailureReportNamesOnly(t *testing.T) {
	file := writeEnvFile(t, "AAA=first-secret\nBBB=second-secret\nCCC=third-secret\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{failOn: "BBB"}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "shop", "", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit when a write fails")
	}
	out := stdout.String() + stderr.String()
	for _, leaked := range []string{"first-secret", "second-secret", "third-secret"} {
		if strings.Contains(out, leaked) {
			t.Errorf("value %q leaked into output: %q", leaked, out)
		}
	}
	// Stops at the failure rather than pushing the rest, and says so.
	if got := strings.Join(rec.calls, " "); got != "AAA=first-secret" {
		t.Errorf("expected the loop to stop at BBB, sent: %q", got)
	}
	for _, want := range []string{"Failed at BBB", "after 1 of 3", "safe"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("expected stderr to mention %q, got: %q", want, stderr.String())
		}
	}
}

// Criterion 8: --dry-run writes nothing, and still prints no values.
func TestSecretsImportCore_DryRunWritesNothing(t *testing.T) {
	file := writeEnvFile(t, "API_KEY=dry-run-secret\nOTHER=another-secret\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "shop", "web", true, rec.set)

	if code != 0 {
		t.Fatalf("dry run should succeed, got exit %d (stderr: %q)", code, stderr.String())
	}
	if len(rec.calls) != 0 {
		t.Errorf("--dry-run must write nothing, but got: %v", rec.calls)
	}
	for _, want := range []string{"API_KEY", "OTHER", "deployment shop, service web", "No secrets were written"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("expected stdout to mention %q, got: %q", want, stdout.String())
		}
	}
	for _, leaked := range []string{"dry-run-secret", "another-secret"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Errorf("value %q leaked into dry-run output: %q", leaked, stdout.String())
		}
	}
}

// Criterion 7: a missing file fails before anything is sent.
func TestSecretsImportCore_MissingFileSendsNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, filepath.Join(t.TempDir(), "nope.env"),
		nil, "", "", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit for a missing file")
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing should have been sent, got: %v", rec.calls)
	}
}

// The --service-requires--deployment guard applies to import too, and trips
// before the file is even read.
func TestSecretsImportCore_ServiceWithoutDeploymentRejected(t *testing.T) {
	file := writeEnvFile(t, "API_KEY=value\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "", "web", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit for --service without --deployment")
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing should have been sent, got: %v", rec.calls)
	}
	if !strings.Contains(stderr.String(), "--service requires --deployment") {
		t.Errorf("expected guard message, got: %q", stderr.String())
	}
}

// -e overrides the file's value for the same key (criterion 4, at the import
// surface rather than in the merge helper).
func TestSecretsImportCore_FlagOverridesFileValue(t *testing.T) {
	file := writeEnvFile(t, "API_KEY=from-file\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, []string{"API_KEY=from-flag"},
		"shop", "", false, rec.set)

	if code != 0 {
		t.Fatalf("expected success, got exit %d (stderr: %q)", code, stderr.String())
	}
	if got := strings.Join(rec.calls, " "); got != "API_KEY=from-flag" {
		t.Errorf("expected the -e flag to win, sent: %q", got)
	}
}

// An empty file is rejected rather than reported as a successful no-op.
func TestSecretsImportCore_EmptyFileRejected(t *testing.T) {
	file := writeEnvFile(t, "# only a comment\n\n")
	var stdout, stderr bytes.Buffer
	rec := &recordingSetter{}

	code := runSecretsImportCore(&stdout, &stderr, file, nil, "", "", false, rec.set)

	if code == 0 {
		t.Errorf("expected non-zero exit for a file with no keys")
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing should have been sent, got: %v", rec.calls)
	}
}
