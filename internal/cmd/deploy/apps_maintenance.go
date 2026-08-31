package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// Maintenance product outcomes extend the shared transport ladder. A recorded
// finding is actionable output rather than a technical failure, but it must be
// distinguishable from found-nothing in automation.
const (
	exitMaintenanceSuccess           = 0
	exitMaintenanceBudgetExhausted   = 10
	exitMaintenanceFindingRecorded   = 11
	exitMaintenanceCancelled         = 12
	exitMaintenanceSkippedConcurrent = 13
)

var maintenancePollInterval = time.Second
var maintenancePollTimeout = 35 * time.Minute

var (
	maintenanceStatusJSON bool
	maintenanceChangeJSON bool
	maintenanceChangeYes  bool
	maintenanceRunJSON    bool
	maintenanceRunAsync   bool
	maintenanceRunFollow  bool
	maintenanceRunMode    string
	maintenanceCheckRunID string
	maintenanceRunKey     string
	maintenanceRunsJSON   bool
	maintenanceRunsLimit  int
)

var appsMaintenanceCmd = &cobra.Command{
	Use:   "maintenance",
	Short: "Operate an app's maintenance agent",
	Long: `Inspect, enable, disable and run the maintenance agent for one app.

Subcommands:
  status <alias>   effective settings and latest run
  enable <alias>   opt this app into scheduled maintenance
  disable <alias>  stop new scheduled maintenance runs
  run <alias>      start one run (--async or --follow)
  runs <alias>     list the run read models, newest first`,
}

var appsMaintenanceStatusCmd = &cobra.Command{
	Use:   "status <alias>",
	Short: "Show maintenance settings and the latest run",
	Args:  cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runAppsMaintenanceStatusCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], maintenanceStatusJSON))
	},
}

var appsMaintenanceEnableCmd = &cobra.Command{
	Use:   "enable <alias>",
	Short: "Enable scheduled maintenance for one app",
	Args:  cobra.ExactArgs(1),
	Run:   func(_ *cobra.Command, args []string) { runAppsMaintenanceChange(args[0], true) },
}

var appsMaintenanceDisableCmd = &cobra.Command{
	Use:   "disable <alias>",
	Short: "Disable scheduled maintenance for one app",
	Args:  cobra.ExactArgs(1),
	Run:   func(_ *cobra.Command, args []string) { runAppsMaintenanceChange(args[0], false) },
}

var appsMaintenanceRunCmd = &cobra.Command{
	Use:   "run <alias>",
	Short: "Start one maintenance run",
	Long: `Start one maintenance run and wait for its terminal read model.

--async returns the server's dispatch document immediately. --follow emits
status changes and, with --json, stable NDJSON ending in exactly one typed
summary object. Reuse --idempotency-key to replay the same intent and receive
the same execution and run ids; omit it for a fresh random key.

Exit codes: 0 success/found_nothing, 11 finding_recorded, 10 budget exhausted,
12 cancelled, 13 skipped_concurrent; 3 auth/permission, 5 validation,
6 conflict, 7 timeout and 1 technical failure.

Examples:
  dibbla apps maintenance run myapp --follow --json
  dibbla apps maintenance run myapp --async --idempotency-key nightly-2026-08-31
  dibbla apps maintenance run myapp --mode check_triage --check-run <id>`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runAppsMaintenanceRunCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0],
			maintenanceRunMode, maintenanceCheckRunID, maintenanceRunKey,
			runMode(maintenanceRunAsync, maintenanceRunFollow), maintenanceRunJSON, time.Now))
	},
}

var appsMaintenanceRunsCmd = &cobra.Command{
	Use:   "runs <alias>",
	Short: "List maintenance runs, newest first",
	Args:  cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		cfg := config.Load()
		requireToken(cfg)
		os.Exit(runAppsMaintenanceRunsCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], maintenanceRunsLimit, maintenanceRunsJSON))
	},
}

func init() {
	appsMaintenanceStatusCmd.Flags().BoolVar(&maintenanceStatusJSON, "json", false, "Print the raw API document")
	for _, cmd := range []*cobra.Command{appsMaintenanceEnableCmd, appsMaintenanceDisableCmd} {
		cmd.Flags().BoolVarP(&maintenanceChangeYes, "yes", "y", false, "Confirm non-interactively")
		cmd.Flags().BoolVar(&maintenanceChangeJSON, "json", false, "Print the raw API acknowledgement")
	}
	appsMaintenanceRunCmd.Flags().BoolVar(&maintenanceRunAsync, "async", false, "Return as soon as dispatch is accepted")
	appsMaintenanceRunCmd.Flags().BoolVar(&maintenanceRunFollow, "follow", false, "Follow status changes to one terminal object")
	appsMaintenanceRunCmd.Flags().BoolVar(&maintenanceRunJSON, "json", false, "Print JSON (NDJSON with --follow)")
	appsMaintenanceRunCmd.Flags().StringVar(&maintenanceRunMode, "mode", "nightly", "Run mode: nightly or check_triage")
	appsMaintenanceRunCmd.Flags().StringVar(&maintenanceCheckRunID, "check-run", "", "Application Check run id (required for check_triage)")
	appsMaintenanceRunCmd.Flags().StringVar(&maintenanceRunKey, "idempotency-key", "", "Stable key to replay this run intent")
	appsMaintenanceRunCmd.MarkFlagsMutuallyExclusive("async", "follow")
	appsMaintenanceRunsCmd.Flags().IntVar(&maintenanceRunsLimit, "limit", 25, "Maximum runs to request")
	appsMaintenanceRunsCmd.Flags().BoolVar(&maintenanceRunsJSON, "json", false, "Print the raw API document")

	appsMaintenanceCmd.AddCommand(appsMaintenanceStatusCmd, appsMaintenanceEnableCmd, appsMaintenanceDisableCmd, appsMaintenanceRunCmd, appsMaintenanceRunsCmd)
}

func runAppsMaintenanceStatusCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	status, raw, err := apps.GetMaintenanceStatus(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "maintenance status", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	state := "disabled"
	if status.Settings.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(stdout, "%s maintenance %s for %s\n", platform.Icon("🔧", "[MAINT]"), state, alias)
	fmt.Fprintf(stdout, "   deployment: %s\n", status.DeploymentID)
	fmt.Fprintf(stdout, "   model:      %s\n", orDash(status.Settings.Model))
	fmt.Fprintf(stdout, "   schedule:   %s (%s)\n", orDash(status.Settings.NightlyCron), orDash(status.Settings.Timezone))
	if status.LastRun != nil {
		fmt.Fprintf(stdout, "   last run:   %s — %s", status.LastRun.ExecutionID, status.LastRun.Status)
		if status.LastRun.TerminalCode != "" {
			fmt.Fprintf(stdout, " (%s)", status.LastRun.TerminalCode)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

func runAppsMaintenanceChange(alias string, enabled bool) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsMaintenanceChangeCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, alias,
		enabled, maintenanceChangeYes, maintenanceChangeJSON, askConfirm))
}

func runAppsMaintenanceChangeCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, enabled, yes, jsonOut bool, confirm func(string) (bool, error)) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	if !yes {
		ok, err := confirm(fmt.Sprintf("%s maintenance for '%s'?", title(verb), alias))
		if err != nil {
			return refuseUnconfirmable(stderr, verb+" maintenance for '"+alias+"'")
		}
		if !ok {
			fmt.Fprintln(stdout, "No changes made.")
			return 0
		}
	}
	current, _, err := apps.GetMaintenanceStatus(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "maintenance "+verb, alias, err)
	}
	ack, raw, err := apps.SetMaintenanceEnabled(apiURL, apiToken, alias, enabled, current.Settings.AppVersion)
	if err != nil {
		return reportAppError(stderr, "maintenance "+verb, alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "%s maintenance %sd for %s (deployment %s)\n",
		platform.Icon("✓", "[OK]"), verb, ack.Alias, ack.DeploymentID)
	return 0
}

func runAppsMaintenanceRunCore(stdout, stderr io.Writer, apiURL, apiToken, alias, mode, checkRunID, key string, runMode checksRunMode, jsonOut bool, now func() time.Time) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	mode = strings.TrimSpace(mode)
	checkRunID = strings.TrimSpace(checkRunID)
	if mode != "nightly" && mode != "check_triage" {
		fmt.Fprintln(stderr, "maintenance run mode must be nightly or check_triage")
		return 5
	}
	if mode == "nightly" && checkRunID != "" {
		fmt.Fprintln(stderr, "nightly mode does not accept --check-run")
		return 5
	}
	if mode == "check_triage" && checkRunID == "" {
		fmt.Fprintln(stderr, "check_triage mode requires --check-run")
		return 5
	}
	key = strings.TrimSpace(key)
	if key == "" {
		var err error
		key, err = apps.NewIdempotencyKey()
		if err != nil {
			fmt.Fprintf(stderr, "maintenance run %s failed: %v\n", alias, err)
			return 1
		}
	}
	dispatch, raw, err := apps.StartMaintenanceRun(apiURL, apiToken, alias, mode, checkRunID, key)
	if err != nil {
		return reportAppError(stderr, "maintenance run", alias, err)
	}
	if runMode == runModeAsync {
		if jsonOut {
			fmt.Fprintln(stdout, string(raw))
		} else {
			fmt.Fprintf(stdout, "%s %s — execution %s, run %s\n", platform.Icon("✓", "[OK]"), dispatch.Code, dispatch.ExecutionID, dispatch.RunID)
			if dispatch.Replayed {
				fmt.Fprintln(stdout, "   replayed: yes — no second run was started")
			}
		}
		return maintenanceDispatchExitCode(dispatch)
	}
	if dispatch.Code == "dispatch_failed" || dispatch.Code == "no_run_id" || dispatch.ExecutionID == "" {
		return printMaintenanceDispatchTerminal(stdout, dispatch, jsonOut)
	}
	execution, _, err := apps.GetMaintenanceRun(apiURL, apiToken, alias, dispatch.ExecutionID)
	if err != nil {
		return reportAppError(stderr, "maintenance run poll", alias, err)
	}
	if runMode == runModeFollow && jsonOut {
		emitMaintenanceEvent(stdout, "execution_created", execution, dispatch, nil)
	} else if !jsonOut {
		fmt.Fprintf(stdout, "%s maintenance execution %s (%s)\n", platform.Icon("⏳", "[..]"), execution.ExecutionID, execution.Status)
		if dispatch.Replayed {
			fmt.Fprintln(stdout, "   replayed: yes — following the original run")
		}
	}
	return pollMaintenanceRun(stdout, stderr, apiURL, apiToken, alias, execution, dispatch, runMode, jsonOut, now)
}

func maintenanceDispatchExitCode(dispatch *apps.MaintenanceDispatch) int {
	switch dispatch.Code {
	case "dispatched", "replayed":
		return 0
	case "budget_exhausted":
		return exitMaintenanceBudgetExhausted
	case "skipped_concurrent":
		return exitMaintenanceSkippedConcurrent
	default:
		return 1
	}
}

func printMaintenanceDispatchTerminal(stdout io.Writer, dispatch *apps.MaintenanceDispatch, jsonOut bool) int {
	code := maintenanceDispatchExitCode(dispatch)
	if jsonOut {
		doc := map[string]any{"schema_version": 1, "type": "summary", "outcome": dispatch.Code, "exit_code": code, "dispatch": dispatch}
		writeJSONDocument(stdout, doc)
	} else {
		fmt.Fprintf(stdout, "%s maintenance run ended: %s\n", platform.Icon("❌", "[X]"), dispatch.Code)
	}
	return code
}

func pollMaintenanceRun(stdout, stderr io.Writer, apiURL, apiToken, alias string, execution *apps.MaintenanceRun, dispatch *apps.MaintenanceDispatch, mode checksRunMode, jsonOut bool, now func() time.Time) int {
	deadline := now().Add(maintenancePollTimeout)
	if !execution.DeadlineAt.IsZero() && execution.DeadlineAt.Before(deadline) {
		deadline = execution.DeadlineAt
	}
	last := execution.Status
	for !execution.IsTerminal() {
		time.Sleep(maintenancePollInterval)
		if now().After(deadline) {
			fmt.Fprintf(stderr, "%s timed out waiting for maintenance execution %s (status %s)\n", platform.Icon("⏱", "[TIMEOUT]"), execution.ExecutionID, execution.Status)
			return 7
		}
		updated, _, err := apps.GetMaintenanceRun(apiURL, apiToken, alias, execution.ExecutionID)
		if err != nil {
			return reportAppError(stderr, "maintenance run poll", alias, err)
		}
		execution = updated
		if execution.Status != last {
			if mode == runModeFollow && jsonOut {
				emitMaintenanceEvent(stdout, "execution_status", execution, dispatch, nil)
			} else if !jsonOut {
				fmt.Fprintf(stdout, "   status: %s\n", execution.Status)
			}
			last = execution.Status
		}
	}
	return printMaintenanceTerminal(stdout, execution, dispatch, mode, jsonOut)
}

func printMaintenanceTerminal(stdout io.Writer, execution *apps.MaintenanceRun, dispatch *apps.MaintenanceDispatch, mode checksRunMode, jsonOut bool) int {
	code := maintenanceExitCode(execution)
	if jsonOut {
		if mode == runModeFollow {
			emitMaintenanceEvent(stdout, "summary", execution, dispatch, &code)
		} else {
			writeJSONDocument(stdout, map[string]any{
				"schema_version": 1, "type": "execution", "execution_id": execution.ExecutionID,
				"outcome": maintenanceOutcome(execution), "exit_code": code, "execution": execution,
			})
		}
		return code
	}
	fmt.Fprintf(stdout, "%s %s — execution %s\n", maintenanceIcon(code), maintenanceOutcome(execution), execution.ExecutionID)
	if execution.TerminalCode != "" {
		fmt.Fprintf(stdout, "   code:        %s\n", execution.TerminalCode)
	}
	if execution.Summary != "" {
		fmt.Fprintf(stdout, "   summary:     %s\n", execution.Summary)
	}
	if execution.Fingerprint != "" {
		fmt.Fprintf(stdout, "   fingerprint: %s\n", execution.Fingerprint)
	}
	if execution.ProposalID != "" {
		fmt.Fprintf(stdout, "   proposal:    %s\n", execution.ProposalID)
	}
	return code
}

func maintenanceOutcome(execution *apps.MaintenanceRun) string {
	if execution.Status == "completed" && strings.EqualFold(execution.TerminalCode, "FINDING_RECORDED") {
		return "finding_recorded"
	}
	if execution.Status == "completed" && execution.TerminalCode != "" {
		return strings.ToLower(execution.TerminalCode)
	}
	return execution.Status
}

func maintenanceExitCode(execution *apps.MaintenanceRun) int {
	switch execution.Status {
	case "completed":
		if strings.EqualFold(execution.TerminalCode, "FINDING_RECORDED") {
			return exitMaintenanceFindingRecorded
		}
		return exitMaintenanceSuccess
	case "budget_exhausted":
		return exitMaintenanceBudgetExhausted
	case "cancelled":
		return exitMaintenanceCancelled
	case "skipped_concurrent":
		return exitMaintenanceSkippedConcurrent
	default:
		return 1
	}
}

func maintenanceIcon(code int) string {
	switch code {
	case 0:
		return platform.Icon("✅", "[OK]")
	case exitMaintenanceFindingRecorded:
		return platform.Icon("🔎", "[FINDING]")
	case exitMaintenanceBudgetExhausted:
		return platform.Icon("⚠️", "[BUDGET]")
	default:
		return platform.Icon("❌", "[X]")
	}
}

func emitMaintenanceEvent(w io.Writer, eventType string, execution *apps.MaintenanceRun, dispatch *apps.MaintenanceDispatch, exitCode *int) {
	doc := map[string]any{
		"schema_version": 1, "type": eventType, "execution_id": execution.ExecutionID,
		"status": execution.Status, "execution": execution,
	}
	if eventType == "execution_created" {
		doc["dispatch"] = dispatch
	}
	if exitCode != nil {
		doc["outcome"] = maintenanceOutcome(execution)
		doc["exit_code"] = *exitCode
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func runAppsMaintenanceRunsCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, limit int, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	if limit < 1 || limit > 100 {
		fmt.Fprintln(stderr, "maintenance runs --limit must be between 1 and 100")
		return 5
	}
	page, raw, err := apps.ListMaintenanceRuns(apiURL, apiToken, alias, limit)
	if err != nil {
		return reportAppError(stderr, "maintenance runs", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if len(page.Runs) == 0 {
		fmt.Fprintf(stdout, "%s no maintenance runs for %s\n", platform.Icon("🔧", "[MAINT]"), alias)
		return 0
	}
	fmt.Fprintf(stdout, "%s maintenance runs for %s\n", platform.Icon("🔧", "[MAINT]"), alias)
	fmt.Fprintf(stdout, "   %-24s %-20s %-22s %-10s %s\n", "EXECUTION", "STATUS", "TERMINAL CODE", "TOKENS", "CREATED")
	for i := range page.Runs {
		run := &page.Runs[i]
		tokens := "-"
		if run.UsedTokens != nil {
			tokens = fmt.Sprintf("%d", *run.UsedTokens)
		}
		fmt.Fprintf(stdout, "   %-24s %-20s %-22s %-10s %s\n", run.ExecutionID, run.Status, orDash(run.TerminalCode), tokens, run.CreatedAt.Local().Format("2006-01-02 15:04"))
		if run.Summary != "" {
			fmt.Fprintf(stdout, "      %s\n", run.Summary)
		}
		if run.ProposalID != "" {
			fmt.Fprintf(stdout, "      proposal %s\n", run.ProposalID)
		}
	}
	return 0
}

// prettyRawObject embeds an API object without losing unknown fields. It is
// used by proposal show --diff, where two server documents form one stable CLI
// document.
func prettyRawObject(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	return append(json.RawMessage(nil), trimmed...)
}
