package wf

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/applogs"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	logsFlagSince   time.Duration
	logsFlagFollow  bool
	logsFlagTail    int
	logsFlagLevel   string
	logsFlagJSON    bool
	logsFlagNoColor bool
)

var runLogsCmd = &cobra.Command{
	Use:   "logs <runId>",
	Short: "Print logs for a workflow run",
	Long: `Print structured logs emitted by workflow-server and go-toolserver
during the execution of a workflow run.

By default prints persisted logs (WARN/ERROR levels) for the run.
With --follow, also streams live INFO/DEBUG entries that aren't persisted.

Examples:
  dibbla wf logs run_abc123
  dibbla wf logs run_abc123 --follow
  dibbla wf logs run_abc123 --level debug -f
  dibbla wf logs run_abc123 --tail 200`,
	Args: cobra.ExactArgs(1),
	RunE: runRunLogs,
}

func init() {
	runLogsCmd.Flags().DurationVar(&logsFlagSince, "since", 15*time.Minute, "Show logs newer than this duration (e.g. 10m, 24h)")
	runLogsCmd.Flags().BoolVarP(&logsFlagFollow, "follow", "f", false, "Stream new log lines as they arrive")
	runLogsCmd.Flags().IntVarP(&logsFlagTail, "tail", "n", 0, "Show only the last N persisted lines (0 = use --since window)")
	runLogsCmd.Flags().StringVar(&logsFlagLevel, "level", "info", "Minimum level to show: debug, info, warn, error")
	runLogsCmd.Flags().BoolVar(&logsFlagJSON, "json", false, "Emit raw NDJSON instead of human-readable lines")
	runLogsCmd.Flags().BoolVar(&logsFlagNoColor, "no-color", false, "Disable color output")

	workflowsCmd.AddCommand(runLogsCmd)
}

func runRunLogs(cmd *cobra.Command, args []string) error {
	runID := strings.TrimSpace(args[0])
	if runID == "" {
		return fmt.Errorf("runId is required")
	}
	return runLogsByID(cmd, runID)
}

// runLogsByID streams run logs to stdout. Used by both `dibbla wf logs <id>`
// and `dibbla wf execute --follow`, which captures the runId from the
// async-execute response and then tails.
func runLogsByID(cmd *cobra.Command, runID string) error {
	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Fprintf(os.Stderr, "%s Error: API token is required. Run `dibbla login` or set DIBBLA_API_TOKEN.\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// When invoked from `wf execute --follow` the logsFlagXxx package globals
	// are at their zero values (cobra only binds them when the user passes
	// `wf logs ...` directly). Pick safe defaults that work for both paths.
	tail := logsFlagTail
	level := logsFlagLevel
	if level == "" {
		level = "debug" // execute --follow wants to see everything
	}
	follow := true // both call sites want a live tail
	if cmd.Flags().Lookup("follow") != nil {
		// `wf logs` users may have explicitly passed --follow=false; respect it.
		if v, err := cmd.Flags().GetBool("follow"); err == nil && cmd.CalledAs() == "logs" {
			follow = v
		}
	}

	opts := applogs.RunOptions{
		Tail:   tail,
		Level:  level,
		Follow: follow,
	}
	if logsFlagSince > 0 && tail == 0 {
		opts.Since = time.Now().Add(-logsFlagSince)
	}

	body, err := applogs.StreamRun(ctx, cfg.APIURL, cfg.APIToken, runID, opts)
	if err != nil {
		var httpErr *applogs.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.Status {
			case 401, 403:
				return fmt.Errorf("not authorized — check your API token (got %d)", httpErr.Status)
			case 404:
				return fmt.Errorf("run %q not found in your organization", runID)
			}
		}
		return err
	}
	defer body.Close()

	useColor := !logsFlagNoColor && !logsFlagJSON && isatty.IsTerminal(os.Stdout.Fd())

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if logsFlagJSON {
			fmt.Println(string(line))
			continue
		}

		entry, ok, derr := applogs.DecodeLine(line)
		if derr != nil {
			fmt.Fprintln(os.Stderr, "logs: "+derr.Error())
			continue
		}
		if !ok {
			continue
		}
		// End-of-run sentinel from the server: print the line, then exit
		// cleanly so `dibbla wf execute --follow` can return.
		if entry.Labels != nil && entry.Labels["event"] == "run_completed" {
			fmt.Println(applogs.FormatEntry(entry, useColor))
			return nil
		}
		fmt.Println(applogs.FormatEntry(entry, useColor))
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("read logs stream: %w", err)
	}
	return nil
}
