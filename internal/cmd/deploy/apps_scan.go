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
	root := &cobra.Command{Use: "scan", Short: "Test Security scan on a source archive without deployment", Long: `Test an existing app's source archive for secrets only. No build, package
installation, project execution or deployment occurs. Dependencies and image/OS
vulnerabilities are not checked; the running app's findings remain unchanged.
Reports can be read repeatedly until end or the absolute TTL (default 60 minutes
from creation). Reads and retries never extend it. End deletes all server copies.
A failed, interrupted or expired scan is never a clean report.`}
	for _, action := range []string{"start", "status", "result", "end"} {
		var jsonOut bool
		var archive, requestID string
		verb := action
		use, short := "<alias> <scan-id>", map[string]string{"start": "Upload tar.gz and test for secrets", "status": "Read temporary Security scan status", "result": "Read the complete temporary Security scan report", "end": "Stop and delete a temporary Security scan"}[verb]
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
			cmd.Long = `Upload an existing tar.gz archive directly for a secrets-only Security scan.
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
Exit 0 means the request succeeded, not that no secrets were found. A failed scan
exits 1 with its status and error_code. Always end the run when finished.

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
		fmt.Fprintf(stdout, "Security scan %s: %s\nCoverage: secrets only\nExpires: %s\n", run.ID, run.State, run.Expires.Format("2006-01-02T15:04:05Z07:00"))
		if run.ErrorCode != "" {
			fmt.Fprintln(stdout, "Error:", run.ErrorCode)
		}
		if action == "result" {
			var report struct {
				Findings []struct {
					File string `json:"file"`
					Line int    `json:"line"`
					Rule string `json:"rule"`
				} `json:"findings"`
			}
			if err = json.Unmarshal(raw, &report); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			for _, f := range report.Findings {
				fmt.Fprintf(stdout, "%s:%d %s\n", f.File, f.Line, f.Rule)
			}
			fmt.Fprintf(stdout, "%d secret findings\n", len(report.Findings))
		}
	}
	if run.State == "failed" {
		return 1
	}
	return 0
}
