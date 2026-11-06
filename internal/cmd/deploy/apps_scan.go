package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

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
expired scan, or a dependency status other than complete, is never a clean report.`}
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
	return root
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
