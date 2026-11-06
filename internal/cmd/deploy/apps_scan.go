package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
)

func init() { appsCmd.AddCommand(newAppsScanCmd()) }
func newAppsScanCmd() *cobra.Command {
	root := &cobra.Command{Use: "scan", Short: "Test Security scan on a source archive without deployment", Long: `Test an existing app's source archive for secrets and for vulnerable
dependency versions in its lock files. No build, package installation, project
script or deployment occurs; the running app's findings remain unchanged.

Dependencies are read from lock files only. Verified with real files:
package-lock.json, pnpm-lock.yaml, bun.lock, go.mod, uv.lock and
requirements*.txt with exact == pins. Every other dependency file is listed
per file and service as a gap (for example bun.lockb, a package.json without a
lock file, or a requirement without an exact version). Image/OS packages, the
language runtime and anything resolved during a build are never covered: only
the Security scan of a deployment checks those.

Reports can be read repeatedly until end or the absolute TTL (default 60 minutes
from creation). Reads and retries never extend it. End deletes all server copies,
including the temporary SBOM and scanner output. A failed, interrupted or
expired scan, or a dependency status other than complete, is never a clean report.

compare puts two reports of the same app side by side, so an agent can tell
what its change fixed from what changed in the scanning itself.

Agent loop: read all deployed findings, test the unchanged source, make the
authorized change, test again, compare, then end both runs. A later deployment
creates a fresh image and secret report; it never promotes a temporary report.
The Dibbla MCP connector offers the same lifecycle through its own file upload
client. Use the connector skill for that surface; no local scanners are needed.`}
	for _, action := range []string{"start", "status", "result", "end"} {
		var jsonOut bool
		var archive, requestID string
		verb := action
		use, short := "<alias> <scan-id>", map[string]string{"start": "Upload tar.gz and test for secrets and dependencies", "status": "Read temporary Security scan status", "result": "Read the complete temporary Security scan report", "end": "Stop and delete a temporary Security scan"}[verb]
		count := 2
		if verb == "start" {
			use = "<alias>"
			count = 1
		}
		cmd := &cobra.Command{Use: verb + " " + use, Short: short, Args: cobra.ExactArgs(count), Run: func(cmd *cobra.Command, args []string) {
			cfg := config.Load()
			requireToken(cfg)
			id := ""
			if len(args) > 1 {
				id = args[1]
			}
			os.Exit(runAppsScanCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, verb, args[0], id, archive, requestID, jsonOut))
		}}
		cmd.Flags().BoolVar(&jsonOut, "json", false, "Print complete machine-readable JSON, including API errors")
		if verb == "start" {
			cmd.Long = `Upload an existing tar.gz archive directly for a temporary Security scan.
Only regular files and directories are allowed; links and unsafe paths fail.
Limits: 50 MiB compressed, 200 MiB expanded including headers, 10,000 entries,
2 minutes for upload + scan, 2 concurrent uploads/scans per installation.
Up to 4 unclosed runs per organization and 16 per installation are retained.
Nested archives, encoded secrets and git history are outside this coverage.

Keep the request ID printed on stderr. Repeat start with the same archive and
--request-id after a lost response; it returns the same run, never a second scan.
Reusing the ID for changed bytes is rejected. New code needs a new request ID.
Request IDs expire after 24 hours and cannot resurrect closed/expired reports.
The command waits for scanning; status/result/end can be used from another shell.
Exit 0 means the request succeeded, not that nothing was found: read the result,
including dependencies.status and its gaps. A failed scan exits 1 with its
status and error_code. Always end the run when finished.

Example: dibbla apps scan start myapp --archive source.tar.gz --json`
			cmd.Flags().StringVar(&archive, "archive", "", "Path to source tar.gz (required; no archive copy is created)")
			cmd.Flags().StringVar(&requestID, "request-id", "", "Reuse the previous start request ID after an interrupted response")
			_ = cmd.MarkFlagRequired("archive")
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(newAppsScanCompareCmd())
	return root
}

func newAppsScanCompareCmd() *cobra.Command {
	var jsonOut, image bool
	cmd := &cobra.Command{Use: "compare <alias> <base-scan-id> <head-scan-id>", Short: "Compare two temporary Security scans of the same app", Args: cobra.ExactArgs(3),
		Long: `Compare two temporary Security scans of the same app: base is the report
before your change, head the one after. Both runs must still exist and be
completed; nothing is stored, so compare before you end them, then end both.

Every finding is one of:
  new            in head, not in base, where both reports fully read the file
  persisting     in both (a dependency is matched by service, package and
                 advisory, so a version bump that still carries the advisory
                 is persisting and shows both versions)
  not_detected   in base, gone from head, where head fully read the file
  unverifiable   in base, but head could not check it (file removed, a gap
                 such as INCOMPLETE_VERSIONS, or a failed dependency part):
                 absence proves nothing
  newly_covered  in head from a file base did not fully read: not caused by
                 your change

attribution says what explains a new or not_detected finding:
submitted_code when both reports used the same vulnerability database and
scanners, otherwise submitted_code_or_database / submitted_code_or_scanner.
limitations lists every reason the comparison is not clean code evidence.

A not_detected secret may still be valid and in git history: rotate it.

--image also re-matches the running app's stored image SBOM against today's
vulnerability database and shows those findings separately, with the
revision and database they come from. Image and OS findings are never
resolved by a source report: only a deployment and its Security scan shows
that. Without a stored SBOM the image section says so. Nothing is built or
deployed, and the app's Security scan, findings and notifications stay as
they are.

A missing, expired, ended or unfinished report is an error naming base or
head, never an empty comparison.

Example: dibbla apps scan compare myapp <base-id> <head-id> --image --json`,
		Run: func(cmd *cobra.Command, args []string) {
			cfg := config.Load()
			requireToken(cfg)
			os.Exit(runAppsScanCompare(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], args[1], args[2], image, jsonOut))
		}}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print complete machine-readable JSON, including API errors")
	cmd.Flags().BoolVar(&image, "image", false, "Also re-match the running app's stored image SBOM, shown separately")
	return cmd
}

func runAppsScanCompare(stdout, stderr io.Writer, apiURL, token, alias, base, head string, image, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	if !scanIDRE.MatchString(base) || !scanIDRE.MatchString(head) {
		fmt.Fprintln(stderr, "invalid scan ID")
		return 5
	}
	if base == head {
		fmt.Fprintln(stderr, "base and head must be two different scans")
		return 5
	}
	cmp, raw, err := apps.CompareTestScans(apiURL, token, alias, base, head, image)
	if err != nil {
		if jsonOut {
			var statusErr *apps.StatusError
			if errors.As(err, &statusErr) && json.Valid([]byte(statusErr.Body)) {
				fmt.Fprintln(stdout, statusErr.Body)
			}
		}
		return reportAppError(stderr, "scan compare", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	printComparison(stdout, cmp)
	return 0
}

var changeTitles = []struct{ change, title string }{
	{"new", "New"},
	{"persisting", "Persisting"},
	{"unverifiable", "Unverifiable (head could not check them; absence proves nothing)"},
	{"newly_covered", "Newly covered (base did not read the file; not caused by the change)"},
	{"not_detected", "Not detected in head"},
}

func printComparison(w io.Writer, c *apps.TestScanComparison) {
	fmt.Fprintf(w, "Security scan comparison for %s\nBase: %s\nHead: %s\n", c.Alias, c.Base.ScanID, c.Head.ScanID)
	db := c.Comparable.Database
	if db == "same" && c.Head.VulnDBBuiltAt != nil {
		db += " (" + c.Head.VulnDBBuiltAt.UTC().Format("2006-01-02T15:04Z") + ")"
	}
	fmt.Fprintf(w, "Vulnerability database: %s · dependency coverage: %s · secret scanner: %s\n", db, c.Comparable.Coverage, c.Comparable.SecretScanner)
	s := c.Summary
	fmt.Fprintf(w, "%d new, %d persisting, %d not detected, %d unverifiable, %d newly covered\n", s.New, s.Persisting, s.NotDetected, s.Unverifiable, s.NewlyCovered)
	for _, group := range changeTitles {
		first := true
		for _, f := range c.Findings {
			if f.Change != group.change {
				continue
			}
			if first {
				fmt.Fprintf(w, "%s:\n", group.title)
				first = false
			}
			fmt.Fprintln(w, "  "+comparedLine(f))
		}
	}
	if len(c.Limitations) > 0 {
		fmt.Fprintln(w, "Limitations:")
		for _, l := range c.Limitations {
			where := ""
			if l.Path != "" {
				where = " " + l.Path + scanServiceLabel(l.Service)
			}
			fmt.Fprintf(w, "  %s%s: %s\n", l.Code, where, l.Message)
		}
	}
	img := c.Image
	fmt.Fprintf(w, "Image background: %s\n", img.Status)
	if img.Origin != nil && img.Origin.DeploymentID != "" {
		fmt.Fprintf(w, "  From running revision %s", img.Origin.DeploymentID)
		if img.Origin.CommitSHA != "" {
			fmt.Fprintf(w, " (commit %s)", img.Origin.CommitSHA)
		}
		if img.VulnDBBuiltAt != nil {
			fmt.Fprintf(w, ", database %s", img.VulnDBBuiltAt.UTC().Format("2006-01-02T15:04Z"))
		}
		fmt.Fprintln(w)
	}
	for _, im := range img.Images {
		fmt.Fprintf(w, "  %s [%s]", im.Image, im.Service)
		if im.Error != "" {
			fmt.Fprintf(w, ": %s", im.Error)
		}
		fmt.Fprintf(w, " — %d findings\n", len(im.Vulnerabilities))
		for _, v := range im.Vulnerabilities {
			fix := "no fix"
			if v.FixedIn != "" {
				fix = "fixed in " + v.FixedIn
			}
			fmt.Fprintf(w, "    %s %s %s@%s (%s) %s\n", v.Severity, v.ID, v.Package, v.Version, fix, imageSourceNote(v.InHeadSource))
		}
	}
	fmt.Fprintln(w, "  "+img.Message)
	fmt.Fprintln(w, c.Retention)
}

func imageSourceNote(s string) string {
	switch s {
	case "present":
		return "— still in the head source report"
	case "os_package":
		return "— OS package"
	default:
		return "— not in the head source report; verify with a deployment"
	}
}

func comparedLine(f apps.ComparedFinding) string {
	var b strings.Builder
	if f.Kind == "secret" {
		fmt.Fprintf(&b, "secret %s %s", f.ID, f.File)
		if f.BaseSecret != nil {
			fmt.Fprintf(&b, " base lines %v", f.BaseSecret.Lines)
		}
		if f.HeadSecret != nil {
			fmt.Fprintf(&b, " head lines %v", f.HeadSecret.Lines)
		}
	} else {
		fmt.Fprintf(&b, "%s %s %s%s", f.Severity, f.ID, f.Package, scanServiceLabel(f.Service))
		if f.BaseDep != nil {
			fmt.Fprintf(&b, " base %s", strings.Join(f.BaseDep.Versions, ","))
		}
		if f.HeadDep != nil {
			fmt.Fprintf(&b, " head %s", strings.Join(f.HeadDep.Versions, ","))
		}
		if f.FixedIn != "" {
			fmt.Fprintf(&b, " (fixed in %s)", f.FixedIn)
		}
	}
	if f.Attribution != "" {
		fmt.Fprintf(&b, " — %s", strings.ReplaceAll(f.Attribution, "_", " "))
	}
	if f.Reason != "" {
		fmt.Fprintf(&b, " — %s", f.Reason)
	}
	return b.String()
}

var scanIDRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var scanRequestRE = regexp.MustCompile(`^[0-9]{10}-[a-f0-9]{32}$`)

func runAppsScanCore(stdout, stderr io.Writer, apiURL, token, action, alias, id, archive, requestID string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	var run *apps.TestScan
	var raw []byte
	var err error
	if action == "start" {
		if requestID == "" {
			requestID, err = apps.NewScanRequestID()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		if !scanRequestRE.MatchString(requestID) {
			fmt.Fprintln(stderr, "invalid request ID")
			return 5
		}
		fmt.Fprintf(stderr, "Security scan request ID: %s (reuse with --request-id when retrying)\n", requestID)
		run, raw, err = apps.StartTestScan(apiURL, token, alias, archive, requestID)
	} else {
		if !scanIDRE.MatchString(id) {
			fmt.Fprintln(stderr, "invalid scan ID")
			return 5
		}
		switch action {
		case "status", "result":
			run, raw, err = apps.ReadTestScan(apiURL, token, alias, id, action == "result")
		case "end":
			run, raw, err = apps.EndTestScan(apiURL, token, alias, id)
		default:
			return 5
		}
	}
	if err != nil {
		if jsonOut {
			var statusErr *apps.StatusError
			if errors.As(err, &statusErr) && json.Valid([]byte(statusErr.Body)) {
				fmt.Fprintln(stdout, statusErr.Body)
			}
		}
		return reportAppError(stderr, "scan "+action, alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
	} else {
		coverage := "secrets and dependencies"
		if run.Coverage == apps.CoverageSecretsOnly {
			coverage = "secrets only"
		}
		fmt.Fprintf(stdout, "Security scan %s: %s\nCoverage: %s\nExpires: %s\n", run.ID, run.State, coverage, run.Expires.Format("2006-01-02T15:04:05Z07:00"))
		if run.ErrorCode != "" {
			fmt.Fprintln(stdout, "Error:", run.ErrorCode)
		}
		if action == "result" {
			var report apps.TestScanReport
			if err = json.Unmarshal(raw, &report); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if run.Coverage == apps.CoverageSecretsAndDependencies && report.Dependencies == nil {
				fmt.Fprintln(stderr, "incomplete Security scan report: the dependency part is missing")
				return 1
			}
			if report.ArchiveSHA256 != "" {
				fmt.Fprintf(stdout, "Archive: sha256:%s\n", report.ArchiveSHA256)
			}
			for _, f := range report.Findings {
				fmt.Fprintf(stdout, "%s:%d %s\n", f.File, f.Line, f.Rule)
			}
			fmt.Fprintf(stdout, "%d secret findings", len(report.Findings))
			if report.Gitleaks != "" {
				fmt.Fprintf(stdout, " (Gitleaks %s)", report.Gitleaks)
			}
			fmt.Fprintln(stdout)
			printDependencies(stdout, &report)
		}
	}
	if run.State == "failed" {
		return 1
	}
	return 0
}

func printDependencies(w io.Writer, report *apps.TestScanReport) {
	d := report.Dependencies
	if d == nil {
		fmt.Fprintln(w, "Dependencies: not checked by this server")
		return
	}
	fmt.Fprintf(w, "Dependencies: %s — %d packages from %d files (Syft %s, Grype %s", d.Status, d.Packages, len(d.Files), d.Syft, d.Grype)
	if d.VulnDBBuiltAt != nil {
		fmt.Fprintf(w, ", database %s", d.VulnDBBuiltAt.UTC().Format("2006-01-02T15:04Z"))
	}
	fmt.Fprintln(w, ")")
	for _, f := range d.Files {
		fmt.Fprintf(w, "  %s%s: %s, %d packages\n", f.Path, scanServiceLabel(f.Service), f.Status, f.Packages)
	}
	for _, v := range d.Vulnerabilities {
		fix := "no fix"
		if v.FixedIn != "" {
			fix = "fixed in " + v.FixedIn
		}
		fmt.Fprintf(w, "%s %s %s@%s (%s) %s%s\n", v.Severity, v.ID, v.Package, v.Version, fix, v.File, scanServiceLabel(v.Service))
	}
	fmt.Fprintf(w, "%d dependency vulnerabilities\n", len(d.Vulnerabilities))
	if len(d.Gaps) > 0 {
		fmt.Fprintf(w, "Not checked (%d gaps):\n", len(d.Gaps))
		for _, g := range d.Gaps {
			where := g.Path
			if where == "" {
				where = "all files"
			}
			fmt.Fprintf(w, "  %s%s: %s %s\n", where, scanServiceLabel(g.Service), g.Code, g.Message)
		}
	}
	if len(d.NotCovered) > 0 {
		fmt.Fprintln(w, "Never covered without a build: image/OS packages, language runtime and Go standard library, dependencies resolved during a build.")
	}
}

func scanServiceLabel(s string) string {
	if s == "" {
		return ""
	}
	return " [" + s + "]"
}
