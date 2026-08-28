package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// Product exit codes (P-0033 Part F). Transport keeps the shared CLI ladder
// (1/3/4/5/6/7, see apps.StatusError.ExitCode), so these are the only codes
// the checks family adds. A failing check is not a command error: CI gates on
// them without scraping output.
const (
	exitCheckPass              = 0
	exitCheckFail              = 8
	exitCheckError             = 9
	exitCheckIndeterminate     = 10
	exitCheckCanceled          = 12
	exitCheckSkippedConcurrent = 13
)

// checkPollInterval is how often `run` (sync and --follow) polls the
// execution endpoint. The endpoint answers the whole parent state, so a short
// interval is one cheap GET. A var (not a const) so tests can shrink it.
var checkPollInterval = time.Second

// checkRunTimeout bounds sync/follow polling when the server gives no usable
// deadline. Most executions are clamped far below this by their own
// deadline_at. Also a var for tests.
var checkRunTimeout = 15 * time.Minute

var (
	checksFlagCheck  string
	checksFlagJSON   bool
	checksFlagQuiet  bool
	checksFlagAsync  bool
	checksFlagFollow bool
	checksFlagSince  time.Duration
	checksFlagLimit  int
	checksFlagYes    bool
)

var appsChecksCmd = &cobra.Command{
	Use:   "checks",
	Short: "Inspect and run application checks",
	Long: `Inspect and run an app's application checks (dibbla-checks.yaml).

The alias is always positional and the check id is always a --check flag,
matching ` + "`apps restart <alias> --service <name>`" + `.

Subcommands:
  list <alias>            definitions and current state
  run <alias>             run all or one check (--async or --follow)
  history <alias>         past runs, newest first
  enable|disable <alias>  start or stop the app's scheduled checks`,
}

var appsChecksListCmd = &cobra.Command{
	Use:   "list <alias>",
	Short: "List an app's check definitions and current state",
	Long: `List the checks configured for one app — kind, schedule,
classification and enabled state — plus whether the app's checks runtime
(nightly schedule) is enabled.

An app whose organization has checks enabled but that ships no
dibbla-checks.yaml lists as configured: false with zero definitions. That is
an answer, not an error: the exit code is still 0.

Examples:
  dibbla apps checks list myapp
  dibbla apps checks list myapp --json | jq .definitions`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsChecksList,
}

var appsChecksRunCmd = &cobra.Command{
	Use:   "run <alias>",
	Short: "Run an app's checks now",
	Long: `Trigger one manual execution of all checks, or of one check with
--check, and wait for the outcome.

By default the command polls the execution until it is terminal and exits
with the product outcome: 0 pass, 8 fail, 9 error, 10 indeterminate,
12 canceled, 13 skipped_concurrent. Transport failures keep the CLI-wide
ladder (3 auth, 4 not found, 5 bad request, 6 conflict, 7 timeout, 1 other).

--async returns as soon as the execution is accepted (exit 0) and prints its
id; watch it in the console or via ` + "`apps checks history`" + `.
--follow keeps polling and emits NDJSON on stdout: one execution_created
line, one execution_status line per status change, and exactly one terminal
summary line carrying outcome and implied exit code.

--quiet prints the execution id only (--async), or id and outcome at the
terminal state (sync/--follow). --quiet and --json are mutually exclusive,
as are --async and --follow.

Examples:
  dibbla apps checks run myapp
  dibbla apps checks run myapp --check home-page
  dibbla apps checks run myapp --async --json
  dibbla apps checks run myapp --follow --json`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsChecksRun,
}

var appsChecksHistoryCmd = &cobra.Command{
	Use:   "history <alias>",
	Short: "Show past check runs, newest first",
	Long: `Show the typed result documents of past runs for one check
(--check <id>) or, without --check, for every configured check merged
into one newest-first list.

--since drops runs that started before the window (applied client-side,
e.g. 24h). --limit caps the result and is also sent as the server-side
page size.

With --check the JSON output is the server's page document verbatim (runs
plus next_cursor). Without --check the CLI merges the pages into one
document with a single runs array.

Examples:
  dibbla apps checks history myapp --check home-page
  dibbla apps checks history myapp --since 24h --limit 20 --json`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsChecksHistory,
}

var appsChecksEnableCmd = &cobra.Command{
	Use:   "enable <alias>",
	Short: "Enable an app's scheduled checks",
	Long: `Enable the app's checks runtime: creates the nightly schedule and
allows manual runs. Requires owner or admin, and the app must have
configured checks. --yes skips the confirmation prompt.

Enablement is app-wide; --check is rejected until the API grows per-check
enablement.`,
	Args: cobra.ExactArgs(1),
	Run:  func(cmd *cobra.Command, args []string) { runAppsChecksToggle(cmd, args, true) },
}

var appsChecksDisableCmd = &cobra.Command{
	Use:   "disable <alias>",
	Short: "Disable an app's scheduled checks",
	Long: `Disable the app's checks runtime: stops new scheduled runs while
preserving definitions and history. Requires owner or admin. --yes skips
the confirmation prompt.

Enablement is app-wide; --check is rejected until the API grows per-check
enablement.`,
	Args: cobra.ExactArgs(1),
	Run:  func(cmd *cobra.Command, args []string) { runAppsChecksToggle(cmd, args, false) },
}

func init() {
	appsChecksListCmd.Flags().BoolVar(&checksFlagJSON, "json", false, "Print the raw API document")

	appsChecksRunCmd.Flags().StringVar(&checksFlagCheck, "check", "", "Run only this check id (default: all)")
	appsChecksRunCmd.Flags().BoolVar(&checksFlagAsync, "async", false, "Return as soon as the execution is accepted")
	appsChecksRunCmd.Flags().BoolVar(&checksFlagFollow, "follow", false, "Poll and emit NDJSON progress plus one terminal summary")
	appsChecksRunCmd.Flags().BoolVarP(&checksFlagQuiet, "quiet", "q", false, "Print only ids and outcomes (script-friendly)")
	appsChecksRunCmd.Flags().BoolVar(&checksFlagJSON, "json", false, "Print machine-readable JSON")
	appsChecksRunCmd.MarkFlagsMutuallyExclusive("quiet", "json")
	appsChecksRunCmd.MarkFlagsMutuallyExclusive("async", "follow")

	appsChecksHistoryCmd.Flags().StringVar(&checksFlagCheck, "check", "", "History for one check id (default: every configured check)")
	appsChecksHistoryCmd.Flags().DurationVar(&checksFlagSince, "since", 0, "Only runs started within this window (e.g. 24h)")
	appsChecksHistoryCmd.Flags().IntVar(&checksFlagLimit, "limit", 0, "Max runs to show (server caps the value)")
	appsChecksHistoryCmd.Flags().BoolVar(&checksFlagJSON, "json", false, "Print one JSON document")

	appsChecksEnableCmd.Flags().StringVar(&checksFlagCheck, "check", "", "Unsupported: enablement is app-wide")
	appsChecksEnableCmd.Flags().BoolVarP(&checksFlagYes, "yes", "y", false, "Skip confirmation prompt")
	appsChecksDisableCmd.Flags().StringVar(&checksFlagCheck, "check", "", "Unsupported: enablement is app-wide")
	appsChecksDisableCmd.Flags().BoolVarP(&checksFlagYes, "yes", "y", false, "Skip confirmation prompt")

	appsChecksCmd.AddCommand(appsChecksListCmd)
	appsChecksCmd.AddCommand(appsChecksRunCmd)
	appsChecksCmd.AddCommand(appsChecksHistoryCmd)
	appsChecksCmd.AddCommand(appsChecksEnableCmd)
	appsChecksCmd.AddCommand(appsChecksDisableCmd)
}

func runAppsChecksList(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsChecksListCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], checksFlagJSON))
}

// runAppsChecksListCore is the testable inner implementation of
// `apps checks list`. Returns the exit code; writes only to the given
// writers.
func runAppsChecksListCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}

	res, raw, err := apps.ListChecks(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "checks list", alias, err)
	}

	if jsonOut {
		// Machine contract mirrors the API: the server document verbatim.
		fmt.Fprintln(stdout, string(raw))
		return 0
	}

	fmt.Fprintf(stdout, "%s application checks for %s\n", platform.Icon("🔍", "[CHECKS]"), alias)
	if res.Settings != nil {
		state := "disabled"
		if res.Settings.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(stdout, "   Runtime: %s (settings version %d)\n", state, res.Settings.Version)
	}
	if !res.Configured {
		fmt.Fprintln(stdout, "   Configured: no — the active deployment has no dibbla-checks.yaml")
		if res.ConfigurationErrorCode != "" {
			fmt.Fprintf(stdout, "   Last sync error: %s — %s\n", res.ConfigurationErrorCode, res.ConfigurationErrorDetail)
		}
		return 0
	}
	if res.ConfigurationErrorCode != "" {
		fmt.Fprintf(stdout, "   %s configuration error: %s — %s\n",
			platform.Icon("⚠", "[!]"), res.ConfigurationErrorCode, res.ConfigurationErrorDetail)
	}
	if res.ConfigRevision != "" {
		fmt.Fprintf(stdout, "   Revision: %s\n", shortRevision(res.ConfigRevision))
	}

	fmt.Fprintf(stdout, "   %d check(s):\n", len(res.Definitions))
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "   %-24s %-16s %-10s %-24s %-8s %s\n",
		"ID", "KIND", "SCHEDULE", "CLASSIFICATION", "ENABLED", "NAME")
	fmt.Fprintf(stdout, "   %-24s %-16s %-10s %-24s %-8s %s\n",
		strings.Repeat("-", 22), strings.Repeat("-", 14), strings.Repeat("-", 8),
		strings.Repeat("-", 22), strings.Repeat("-", 6), strings.Repeat("-", 4))
	for _, def := range res.Definitions {
		enabled := "no"
		if def.Enabled {
			enabled = "yes"
		}
		fmt.Fprintf(stdout, "   %-24s %-16s %-10s %-24s %-8s %s\n",
			def.ID, def.Kind, def.Schedule, def.Classification, enabled, def.Name)
	}
	return 0
}

func runAppsChecksRun(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsChecksRunCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0],
		checksFlagCheck, runMode(checksFlagAsync, checksFlagFollow), checksFlagQuiet, checksFlagJSON, time.Now))
}

type checksRunMode int

const (
	runModeSync checksRunMode = iota
	runModeAsync
	runModeFollow
)

func runMode(async, follow bool) checksRunMode {
	switch {
	case async:
		return runModeAsync
	case follow:
		return runModeFollow
	default:
		return runModeSync
	}
}

// runAppsChecksRunCore is the testable inner implementation of
// `apps checks run`. Returns the exit code: a product outcome code when the
// execution reaches a terminal state, a ladder code on transport failure, or
// 7 when polling exceeds the deadline.
func runAppsChecksRunCore(stdout, stderr io.Writer, apiURL, apiToken, alias, checkID string, mode checksRunMode, quiet, jsonOut bool, now func() time.Time) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	var ids []string
	if checkID != "" {
		if !apps.CheckIDRe.MatchString(checkID) {
			fmt.Fprintf(stderr, "%s check id %q does not match %s\n",
				platform.Icon("❌", "[X]"), checkID, apps.CheckIDRe.String())
			return 5
		}
		ids = []string{checkID}
	}

	execution, raw, err := apps.RunChecks(apiURL, apiToken, alias, ids)
	if err != nil {
		return reportAppError(stderr, "checks run", alias, err)
	}

	if mode == runModeAsync {
		return reportAcceptedExecution(stdout, alias, execution, raw, quiet, jsonOut)
	}

	if !quiet && !jsonOut {
		fmt.Fprintf(stdout, "%s running %d check(s) — execution %s (%s)\n",
			platform.Icon("⏳", "[..]"), len(execution.RequestedCheckIDs), execution.ID, execution.Status)
	}
	if mode == runModeFollow && jsonOut {
		// NDJSON contract: one created line, one line per status change,
		// exactly one terminal summary.
		emitFollowEvent(stdout, "execution_created", execution)
	}

	execution, code := pollExecution(stdout, stderr, apiURL, apiToken, alias, execution, mode, quiet, jsonOut, now)
	return code
}

// reportAcceptedExecution prints the --async result and returns the exit
// code: acceptance is success even though nothing has run yet.
func reportAcceptedExecution(stdout io.Writer, alias string, execution *apps.CheckExecution, raw []byte, quiet, jsonOut bool) int {
	switch {
	case jsonOut:
		// The parent execution document as answered by the run endpoint —
		// clients poll this execution id.
		printEmbeddedExecution(stdout, raw)
	case quiet:
		fmt.Fprintln(stdout, execution.ID)
	default:
		fmt.Fprintf(stdout, "%s execution %s accepted (%s)\n",
			platform.Icon("✓", "[OK]"), execution.ID, execution.Status)
		fmt.Fprintf(stdout, "  follow it in the console, or: dibbla apps checks history %s\n", alias)
	}
	return exitCheckPass
}

// pollExecution drives sync and follow modes to a terminal state and prints
// the final result. Returns the terminal execution and its product exit code.
func pollExecution(stdout, stderr io.Writer, apiURL, apiToken, alias string, execution *apps.CheckExecution, mode checksRunMode, quiet, jsonOut bool, now func() time.Time) (*apps.CheckExecution, int) {
	deadline := now().Add(checkRunTimeout)
	if !execution.DeadlineAt.IsZero() && execution.DeadlineAt.Before(deadline) {
		// The server says the run must end by its own deadline; polling past
		// it cannot produce a new answer, so the earlier bound wins.
		deadline = execution.DeadlineAt
	}

	if execution.IsTerminal() {
		return execution, printTerminal(stdout, alias, execution, mode, quiet, jsonOut)
	}

	last := execution.Status
	for {
		time.Sleep(checkPollInterval)
		if now().After(deadline) {
			fmt.Fprintf(stderr, "%s timed out waiting for execution %s (status %s)\n",
				platform.Icon("⏱", "[TIMEOUT]"), execution.ID, execution.Status)
			fmt.Fprintf(stderr, "  it keeps running server-side; watch it in the console or via: dibbla apps checks history %s\n", alias)
			return execution, 7
		}
		updated, err := apps.GetExecution(apiURL, apiToken, alias, execution.ID)
		if err != nil {
			return execution, reportAppError(stderr, "checks run poll", alias, err)
		}
		execution = updated
		if execution.Status != last {
			if jsonOut && mode == runModeFollow {
				emitFollowEvent(stdout, "execution_status", execution)
			} else if !quiet && !jsonOut {
				fmt.Fprintf(stdout, "   status: %s\n", execution.Status)
			}
			last = execution.Status
		}
		if execution.IsTerminal() {
			return execution, printTerminal(stdout, alias, execution, mode, quiet, jsonOut)
		}
	}
}

// printTerminal renders the terminal state for each output mode and returns
// the product exit code.
func printTerminal(stdout io.Writer, alias string, execution *apps.CheckExecution, mode checksRunMode, quiet, jsonOut bool) int {
	code := exitCodeForExecution(execution)
	if jsonOut {
		if mode == runModeFollow {
			// The single terminal line of the NDJSON stream.
			emitFollowEvent(stdout, "summary", execution, code)
			return code
		}
		// Sync --json: one document carrying outcome and the implied exit
		// code next to the execution, so CI never scrapes prose.
		writeJSONDocument(stdout, map[string]any{
			"schema_version": 1,
			"type":           "execution",
			"execution_id":   execution.ID,
			"outcome":        execution.Status,
			"exit_code":      code,
			"execution":      execution,
		})
		return code
	}
	if quiet {
		// id + outcome: enough to gate on without parsing prose.
		fmt.Fprintf(stdout, "%s %s\n", execution.ID, execution.Status)
		return code
	}

	icon, label := outcomeIcon(execution.Status)
	fmt.Fprintf(stdout, "%s %s — execution %s (%d check(s))\n",
		icon, label, execution.ID, len(execution.RequestedCheckIDs))
	if execution.TerminalCode != "" {
		fmt.Fprintf(stdout, "   code: %s\n", execution.TerminalCode)
	}
	if execution.FinishedAt != nil && execution.StartedAt != nil {
		fmt.Fprintf(stdout, "   took: %s\n", execution.FinishedAt.Sub(*execution.StartedAt).Round(time.Second))
	}
	fmt.Fprintf(stdout, "   history: dibbla apps checks history %s\n", alias)
	return code
}

// emitFollowEvent writes one NDJSON line of the documented --follow stream.
// Every line is a complete JSON document; the summary line additionally
// carries the outcome and the implied exit code.
func emitFollowEvent(w io.Writer, eventType string, execution *apps.CheckExecution, exitCodes ...int) {
	doc := map[string]any{
		"schema_version": 1,
		"type":           eventType,
		"execution_id":   execution.ID,
		"status":         execution.Status,
		"execution":      execution,
	}
	if eventType == "summary" && len(exitCodes) > 0 {
		doc["outcome"] = execution.Status
		doc["exit_code"] = exitCodes[0]
	}
	_ = json.NewEncoder(w).Encode(doc)
}

// printEmbeddedExecution extracts the execution object the run endpoint
// answered with and prints it as one pretty JSON document (the wrapper key is
// transport, not product). An unexpected shape falls back to the raw body.
func printEmbeddedExecution(stdout io.Writer, raw []byte) {
	var wrapped struct {
		Execution json.RawMessage `json:"execution"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || len(wrapped.Execution) == 0 {
		fmt.Fprintln(stdout, string(raw))
		return
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, wrapped.Execution, "", "  "); err != nil {
		fmt.Fprintln(stdout, string(wrapped.Execution))
		return
	}
	fmt.Fprintln(stdout, pretty.String())
}

// exitCodeForExecution maps a parent execution's terminal state to the
// product exit codes: 0 pass, 8 fail, 9 error, 10 indeterminate, 12 canceled,
// 13 skipped_concurrent. Non-terminal states never reach this mapping; an
// unknown terminal (a newer server) degrades to 1 rather than guessing.
func exitCodeForExecution(execution *apps.CheckExecution) int {
	switch execution.Status {
	case "pass":
		return exitCheckPass
	case "fail":
		return exitCheckFail
	case "error":
		return exitCheckError
	case "indeterminate":
		return exitCheckIndeterminate
	case "canceled":
		return exitCheckCanceled
	case "skipped_concurrent":
		return exitCheckSkippedConcurrent
	}
	return 1
}

// outcomeIcon pairs each terminal state with an icon plus a text label —
// status is never conveyed by glyph alone.
func outcomeIcon(status string) (string, string) {
	switch status {
	case "pass":
		return platform.Icon("✅", "[PASS]"), "pass"
	case "fail":
		return platform.Icon("❌", "[FAIL]"), "fail"
	case "error":
		return platform.Icon("💥", "[ERROR]"), "error"
	case "indeterminate":
		return platform.Icon("❓", "[INDETERMINATE]"), "indeterminate"
	case "canceled":
		return platform.Icon("🚫", "[CANCELED]"), "canceled"
	case "skipped_concurrent":
		return platform.Icon("⏭", "[SKIPPED]"), "skipped_concurrent"
	}
	return platform.Icon("•", "[?]"), status
}

func runAppsChecksHistory(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsChecksHistoryCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0],
		checksFlagCheck, checksFlagSince, checksFlagLimit, checksFlagJSON, time.Now))
}

// runAppsChecksHistoryCore is the testable inner implementation of
// `apps checks history`. Returns the exit code.
func runAppsChecksHistoryCore(stdout, stderr io.Writer, apiURL, apiToken, alias, checkID string, since time.Duration, limit int, jsonOut bool, now func() time.Time) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	if checkID != "" && !apps.CheckIDRe.MatchString(checkID) {
		fmt.Fprintf(stderr, "%s check id %q does not match %s\n",
			platform.Icon("❌", "[X]"), checkID, apps.CheckIDRe.String())
		return 5
	}

	// Each run travels as a pair: the parsed form the CLI sorts, filters and
	// tabulates by, and the server's own document, which is what --json emits.
	// Re-encoding the parsed form is what made this command drop evidence_refs
	// and evidence_gaps while inventing zero-valued measurements for runs that
	// had not finished (DIB-460); apps.CheckRun is a mirror of deploy-api's
	// view and any field it does not model disappears without an error.
	var runs []historyRun
	var rawPage []byte
	if checkID != "" {
		page, raw, err := apps.GetCheckRuns(apiURL, apiToken, alias, checkID, limit)
		if err != nil {
			return reportAppError(stderr, "checks history", alias, err)
		}
		runs, rawPage = pairRuns(page), raw
	} else {
		// No app-wide history endpoint exists; merge each check's page.
		res, _, err := apps.ListChecks(apiURL, apiToken, alias)
		if err != nil {
			return reportAppError(stderr, "checks history", alias, err)
		}
		if len(res.Definitions) == 0 {
			if jsonOut {
				writeJSONDocument(stdout, map[string]any{
					"schema_version": 1, "deployment_alias": alias, "runs": []json.RawMessage{},
				})
			} else {
				fmt.Fprintf(stdout, "%s no checks configured for %s\n", platform.Icon("🔍", "[CHECKS]"), alias)
			}
			return 0
		}
		for _, def := range res.Definitions {
			page, _, err := apps.GetCheckRuns(apiURL, apiToken, alias, def.ID, limit)
			if err != nil {
				return reportAppError(stderr, "checks history", alias, err)
			}
			runs = append(runs, pairRuns(page)...)
		}
	}

	// Newest first, then the caller's window and cap. --check pages come
	// back already newest-first from the server; sorting again is harmless
	// and keeps the merged path honest.
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].parsed.StartedAt.After(runs[j].parsed.StartedAt) })
	if since > 0 {
		cutoff := now().Add(-since)
		kept := runs[:0]
		for _, run := range runs {
			if !run.parsed.StartedAt.Before(cutoff) {
				kept = append(kept, run)
			}
		}
		runs = kept
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}

	if jsonOut {
		if checkID != "" {
			// One check: the server's page document verbatim.
			fmt.Fprintln(stdout, string(rawPage))
			return 0
		}
		// Merged: N pages cannot share one next_cursor, so this path keeps its
		// own envelope. The runs inside it are the server's documents, key for
		// key and in the server's order — the envelope differs by design, the
		// run documents do not differ at all.
		writeJSONDocument(stdout, map[string]any{
			"schema_version": 1, "deployment_alias": alias, "runs": rawRunsOf(runs),
		})
		return 0
	}

	fmt.Fprintf(stdout, "%s check run history for %s\n", platform.Icon("🕓", "[HISTORY]"), alias)
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "   no runs in the selected window")
		return 0
	}
	fmt.Fprintf(stdout, "   %-18s %-20s %-14s %-22s %s\n",
		"STARTED", "CHECK", "OUTCOME", "CODE", "SUMMARY")
	fmt.Fprintf(stdout, "   %-18s %-20s %-14s %-22s %s\n",
		strings.Repeat("-", 16), strings.Repeat("-", 18), strings.Repeat("-", 12),
		strings.Repeat("-", 20), strings.Repeat("-", 7))
	for _, run := range runs {
		summary := run.parsed.Summary
		if len(summary) > 48 {
			summary = summary[:47] + "…"
		}
		fmt.Fprintf(stdout, "   %-18s %-20s %-14s %-22s %s\n",
			run.parsed.StartedAt.Local().Format("2006-01-02 15:04"),
			run.parsed.CheckID, run.parsed.Outcome, run.parsed.Code, summary)
	}
	return 0
}

// historyRun couples one parsed run with the server document it was parsed
// from, so ordering and filtering can use the typed fields while --json still
// emits bytes the CLI never rebuilt.
type historyRun struct {
	parsed apps.CheckRun
	raw    json.RawMessage
}

// pairRuns zips a page's parsed runs with their raw documents. A page whose
// raw half is short (an older server, or a body that parsed loosely) falls back
// to re-encoding that one run rather than emitting nothing — the fallback is
// the old lossy behaviour, but only for a run the server did not give us bytes
// for, and never silently for a run it did.
func pairRuns(page *apps.CheckRunsPage) []historyRun {
	paired := make([]historyRun, 0, len(page.Runs))
	for i, run := range page.Runs {
		pair := historyRun{parsed: run}
		if i < len(page.RawRuns) {
			pair.raw = page.RawRuns[i]
		}
		paired = append(paired, pair)
	}
	return paired
}

func rawRunsOf(runs []historyRun) []json.RawMessage {
	raw := make([]json.RawMessage, 0, len(runs))
	for _, run := range runs {
		if len(run.raw) > 0 {
			raw = append(raw, run.raw)
			continue
		}
		encoded, err := json.Marshal(run.parsed)
		if err != nil {
			continue
		}
		raw = append(raw, encoded)
	}
	return raw
}

func runAppsChecksToggle(cmd *cobra.Command, args []string, enable bool) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsChecksToggleCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0],
		checksFlagCheck, checksFlagYes, enable, askConfirm))
}

// runAppsChecksToggleCore is the testable inner implementation of
// `apps checks enable|disable`. confirm is the interactive prompt; --yes
// short-circuits it. Returns the exit code.
func runAppsChecksToggleCore(stdout, stderr io.Writer, apiURL, apiToken, alias, checkID string, yes, enable bool, confirm func(string) (bool, error)) int {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	// Per-check enablement has no API yet; refuse locally so no request is
	// ever sent — the same zero-request contract as apps restart's service
	// name validation.
	if checkID != "" {
		fmt.Fprintf(stderr, "%s per-check enablement is not available yet; '%s' applies to the app's whole checks runtime. Omit --check.\n",
			platform.Icon("❌", "[X]"), verb)
		return 5
	}

	if !yes {
		effect := "stop scheduled runs"
		if enable {
			effect = "start scheduled nightly runs"
		}
		ok, err := confirm(fmt.Sprintf("%s application checks for '%s'? This will %s.", title(verb), alias, effect))
		if err != nil {
			// Not the same as a declined prompt: nobody was asked. Saying
			// "Cancelled." and exiting 0 here handed every script and coding
			// agent a green for work that never happened.
			return refuseUnconfirmable(stderr, fmt.Sprintf("%s application checks for '%s'", verb, alias))
		}
		if !ok {
			fmt.Fprintln(stdout, "Cancelled.")
			return 0
		}
	}

	settings, _, err := apps.SetChecksEnabled(apiURL, apiToken, alias, enable)
	if err != nil {
		return reportAppError(stderr, "checks "+verb, alias, err)
	}

	state := "disabled"
	if settings.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(stdout, "%s application checks %s for %s (settings version %d)\n",
		platform.Icon("✓", "[OK]"), state, alias, settings.Version)
	return 0
}

func invalidAlias(stderr io.Writer, alias string) int {
	fmt.Fprintf(stderr, "%s alias %q does not match %s\n",
		platform.Icon("❌", "[X]"), alias, apps.AliasRe.String())
	return 5
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func shortRevision(rev string) string {
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12] + "…"
}

func writeJSONDocument(w io.Writer, doc any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}
