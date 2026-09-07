package wf

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

	return tailLogStream(cmd, fmt.Sprintf("run %q", runID),
		func(ctx context.Context, cfg *config.Config) (io.ReadCloser, error) {
			return applogs.StreamRun(ctx, cfg.APIURL, cfg.APIToken, runID, opts)
		})
}

// logStreamOpener opens one NDJSON log stream against the configured API.
type logStreamOpener func(ctx context.Context, cfg *config.Config) (io.ReadCloser, error)

// tailLogStream prints one run-shaped NDJSON log stream to stdout, honouring
// --json / --no-color and stopping at the server's run_completed sentinel.
// Runs and tool invocations (DIB-764) share it: they differ only in which
// endpoint is opened, which is what `open` decides. `what` names the subject
// in the not-found message ("run \"abc\"", "invocation \"abc\"").
func tailLogStream(cmd *cobra.Command, what string, open logStreamOpener) error {
	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Fprintf(os.Stderr, "%s Error: API token is required. Run `dibbla login` or set DIBBLA_API_TOKEN.\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	parent := cmd.Context()
	if parent == nil {
		// cobra sets a context on Execute; a direct RunE call has none.
		parent = context.Background()
	}
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	body, err := open(ctx, cfg)
	if err != nil {
		var httpErr *applogs.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.Status {
			case 401, 403:
				return fmt.Errorf("not authorized — check your API token (got %d)", httpErr.Status)
			case 404:
				return fmt.Errorf("%s not found in your organization", what)
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
