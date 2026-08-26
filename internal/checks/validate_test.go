package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSource = `version: 1
checks:
  - id: home-page
    name: Home page is usable
    description: The home page is the first thing a customer reaches; if this fails the app is down for everyone.
    kind: http_sequence
    steps:
      - request: {method: GET, route: public, path: /}
        expect: {status: 200, body_contains: Welcome}
`

func write(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestVendoredSchemaHasNotDrifted is the local half of the provenance story.
// It cannot see a change upstream — the two repositories share no CI — but it
// does turn an accidental edit here into a build failure instead of a silent
// change to what this command accepts.
func TestVendoredSchemaHasNotDrifted(t *testing.T) {
	if SchemaDigest() != SourceDigest {
		t.Fatalf("vendored schema changed: %s != recorded %s\n"+
			"If this was deliberate, re-copy from app-hosting-service and update BOTH "+
			"SourceDigest and SourceCommit — a digest bump without a commit bump hides where it came from.",
			SchemaDigest(), SourceDigest)
	}
	if SourceCommit == "" || strings.Contains(SourceCommit, "PLACEHOLDER") {
		t.Fatalf("SourceCommit must name a real app-hosting-service commit, got %q", SourceCommit)
	}
}

func TestAbsentFileIsNotAFailure(t *testing.T) {
	report, err := Validate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if report.Present {
		t.Fatal("an absent dibbla-checks.yaml must not be reported as present")
	}
	if len(report.Findings) != 0 {
		t.Fatalf("absence must produce no findings, got %+v", report.Findings)
	}
}

func TestValidFilePassesAndCarriesItsProvenance(t *testing.T) {
	report, err := Validate(write(t, validSource))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("valid file rejected: %+v", report.Findings)
	}
	if report.SourceCommit != SourceCommit {
		t.Fatalf("a green answer must say which schema it used, got %q", report.SourceCommit)
	}
	if len(report.Unverified) == 0 {
		t.Fatal("a green answer must still say what it could not check")
	}
	if len(report.CheckIDs) != 1 || report.CheckIDs[0] != "home-page" {
		t.Fatalf("check ids: %+v", report.CheckIDs)
	}
}

func TestMissingDescriptionIsRejectedAndNamesTheCheck(t *testing.T) {
	source := strings.Replace(validSource,
		"    description: The home page is the first thing a customer reaches; if this fails the app is down for everyone.\n", "", 1)
	report, err := Validate(write(t, source))
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid {
		t.Fatal("a check with no description must be rejected locally too")
	}
	first := report.Findings[0]
	if first.Code != CodeDescriptionRequired || !strings.Contains(first.Detail, "home-page") {
		t.Fatalf("want %s naming the check, got %+v", CodeDescriptionRequired, first)
	}
}

func TestMissingDescriptionNamesTheRightCheckAmongMany(t *testing.T) {
	source := `version: 1
checks:
  - id: first
    name: First
    description: Why first exists.
    kind: http_sequence
    steps: [{request: {method: GET, route: public, path: /}, expect: {status: 200}}]
  - id: second
    name: Second
    description: Why second exists.
    kind: http_sequence
    steps: [{request: {method: GET, route: public, path: /b}, expect: {status: 200}}]
  - id: third
    name: Third
    kind: http_sequence
    steps: [{request: {method: GET, route: public, path: /c}, expect: {status: 200}}]
`
	report, err := Validate(write(t, source))
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid {
		t.Fatal("must be rejected")
	}
	detail := report.Findings[0].Detail
	if !strings.Contains(detail, "third") {
		t.Fatalf("must name the third check, not an index: %q", detail)
	}
	if strings.Contains(detail, "first") || strings.Contains(detail, "second") {
		t.Fatalf("must not implicate the checks that are fine: %q", detail)
	}
}

func TestLoaderOnlyRulesAreMirrored(t *testing.T) {
	t.Run("click without identity_grant", func(t *testing.T) {
		report, err := Validate(write(t, `version: 1
checks:
  - id: signup
    name: A visitor can sign up
    description: Signup is the only way anyone becomes a customer.
    kind: browser_journey
    steps:
      - click: {control: submit}
`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Valid || !strings.Contains(report.Findings[0].Detail, "identity_grant") {
			t.Fatalf("the identity_grant rule must be mirrored: %+v", report)
		}
	})

	t.Run("literal value on a credential-looking header", func(t *testing.T) {
		report, err := Validate(write(t, `version: 1
checks:
  - id: api
    name: The API answers
    description: The API is the product; if it stops answering nothing works.
    kind: http_sequence
    steps:
      - request:
          method: GET
          route: public
          path: /api
          headers:
            Authorization: {literal: "Bearer hunter2"}
        expect: {status: 200}
`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Valid || report.Findings[0].Code != CodeInlineSecret {
			t.Fatalf("the inline-secret rule must be mirrored: %+v", report)
		}
	})
}

// TestUndecidableRulesAreReportedNotFailed is the asymmetry this package exists
// to hold. A route name that does not exist on the deployment is a real error —
// but only the server can know it. Reporting it locally as invalid would stop a
// deploy that would have succeeded, which teaches people to ignore the tool.
func TestUndecidableRulesAreReportedNotFailed(t *testing.T) {
	report, err := Validate(write(t, strings.Replace(validSource, "route: public", "route: some-route-only-the-server-knows", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("an unknown route must NOT be a local failure: %+v", report.Findings)
	}
	joined := strings.Join(report.Unverified, " | ")
	if !strings.Contains(joined, "route names") {
		t.Fatalf("...and it must be named as unverified: %q", joined)
	}
}

func TestUnreadableShapesFallThroughToTheSchema(t *testing.T) {
	report, err := Validate(write(t, "version: 1\nchecks: {not: an-array}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid {
		t.Fatal("a malformed document must still be rejected")
	}
	if report.Findings[0].Code != CodeInvalid {
		t.Fatalf("a shape the probe cannot read is the schema's error to report, got %+v", report.Findings[0])
	}
}

func TestUnsupportedVersionSaysSo(t *testing.T) {
	report, err := Validate(write(t, strings.Replace(validSource, "version: 1", "version: 2", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.Findings[0].Code != CodeUnsupported {
		t.Fatalf("want %s, got %+v", CodeUnsupported, report.Findings)
	}
}
