package deploy

import (
	"fmt"
	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
	"io"
	"os"
)

func init() { appsCmd.AddCommand(newAppsFindingsCmd()) }

func newAppsFindingsCmd() *cobra.Command {
	var opts apps.FindingsOptions
	var jsonOut, all bool
	cmd := &cobra.Command{Use: "findings <alias>", Short: "Read Security scan findings, status and revision", Args: cobra.ExactArgs(1),
		Long: `Read the existing Security scan; this does not start a scan or change findings.
Default: one page of 50 findings. Use --all to collect every page atomically,
or pass next_cursor as --cursor until has_more=false. A cursor keeps its revision
and filters. SCAN_CHANGED (exit 6) means discard all pages and restart.

--min-severity includes that severity and above. --fixable-only keeps
vulnerabilities with a fix version AND secrets. --service matches exactly and
excludes source secrets (which have no service). --kind selects one finding kind.
JSON contains all finding fields, scan_id, total, filters, status, errors,
revision, timestamps and whether the scan applies to the running revision.
Missing, running, partial, failed or unavailable never means clean. Exit 0 means
the read succeeded, not that the app is secure. --all and --cursor are exclusive.

Examples:
  dibbla apps findings myapp --all --json
  dibbla apps findings myapp --min-severity high --limit 10 --json
  dibbla apps findings myapp --cursor '<next_cursor>' --json`,
		Run: func(cmd *cobra.Command, args []string) {
			opts.FixableOnlySet = cmd.Flags().Changed("fixable-only")
			if opts.Limit < 1 || opts.Limit > 200 {
				fmt.Fprintln(os.Stderr, "limit must be between 1 and 200")
				os.Exit(5)
			}
			cfg := config.Load()
			requireToken(cfg)
			os.Exit(runAppsFindingsCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], opts, jsonOut, all))
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.DeploymentID, "deployment-id", "", "Read a specific revision (default running, else latest marked running=false)")
	f.StringVar(&opts.MinSeverity, "min-severity", "", "critical, high, medium, low, negligible or unknown; always includes secrets")
	f.BoolVar(&opts.FixableOnly, "fixable-only", false, "Keep vulnerabilities with a fix version, plus secrets")
	f.StringVar(&opts.Kind, "kind", "", "vulnerability or secret (default both)")
	f.StringVar(&opts.Service, "service", "", "Exact service name (source secrets have no service)")
	f.IntVar(&opts.Limit, "limit", 50, "Findings per page, 1-200")
	f.StringVar(&opts.Cursor, "cursor", "", "next_cursor from the previous page")
	f.BoolVar(&jsonOut, "json", false, "Print machine-readable JSON without a finding limit beyond pagination")
	f.BoolVar(&all, "all", false, "Collect every page; emit nothing if a page fails or the scan changes")
	cmd.MarkFlagsMutuallyExclusive("all", "cursor")
	return cmd
}

func runAppsFindingsCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, opts apps.FindingsOptions, jsonOut, all bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	get := apps.GetFindings
	if all {
		get = apps.AllFindings
	}
	doc, raw, err := get(apiURL, apiToken, alias, opts)
	if err != nil {
		return reportAppError(stderr, "findings", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "Security scan: %s — %s\nRevision: %s; running: %t (current %s)\nScan: %s\nStarted: %v; completed: %v; rematched: %v\n", alias, doc.Status, doc.DeploymentID, doc.Running, doc.RunningDeploymentID, doc.ScanID, doc.StartedAt, doc.CompletedAt, doc.RematchedAt)
	if doc.Status != "completed" || !doc.Running {
		fmt.Fprintln(stdout, "This is not a complete result for the running revision.")
	}
	for _, e := range doc.Errors {
		fmt.Fprintln(stdout, "Error:", e)
	}
	fmt.Fprintf(stdout, "Findings: %d of %d (offset %d)\n", len(doc.Findings), doc.Total, doc.Offset)
	for _, f := range doc.Findings {
		if f.Kind == "secret" {
			fmt.Fprintf(stdout, "%s %s %s:%d — %s (fingerprint %s)\n", f.Severity, f.ID, f.File, f.Line, f.Description, f.Fingerprint)
		} else {
			fmt.Fprintf(stdout, "%s %s %s@%s [%s; %s] fix=%s (%s) advisory=%s\n", f.Severity, f.ID, f.Package, f.Version, f.Service, f.Type, f.FixedIn, f.FixState, f.Advisory)
		}
	}
	if doc.HasMore {
		fmt.Fprintf(stdout, "More findings: repeat with --cursor '%s' or restart with --all.\n", doc.NextCursor)
	}
	return 0
}
