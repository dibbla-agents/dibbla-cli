package logs

import (
	"bufio"
	"context"
	"encoding/json"
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
	flagSince     time.Duration
	flagFollow    bool
	flagTail      int
	flagGrep      string
	flagJSON      bool
	flagNoColor   bool
	flagLimit     int
	flagService   string
	flagPodStream bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <app>",
	Short: "Print logs for a deployed app",
	Long: `Print logs for one of your deployed apps.

By default prints the last 15 minutes of logs and exits.

Use -f / --follow to stream new lines as they arrive.
Use -n / --tail N to print only the last N lines.

Multi-service:
  --service <name>  scopes the query to one service (Loki source).
  --pod-stream      switches to the K8s pod-log endpoint, useful when Loki
                    is not configured. Requires --service. Output is
                    text/plain prefixed with "[<pod>] " per line.

Examples:
  dibbla logs expense-reporter
  dibbla logs expense-reporter --since 24h
  dibbla logs expense-reporter --since 10m -f
  dibbla logs expense-reporter -n 200
  dibbla logs expense-reporter --grep "timeout"
  dibbla logs expense-reporter --json | jq .
  dibbla logs myapp --service worker -f
  dibbla logs myapp --service web --pod-stream -f`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().DurationVar(&flagSince, "since", 15*time.Minute, "Show logs newer than this duration (e.g. 10m, 24h)")
	logsCmd.Flags().BoolVarP(&flagFollow, "follow", "f", false, "Stream new log lines as they arrive")
	logsCmd.Flags().IntVarP(&flagTail, "tail", "n", 0, "Show only the last N lines (0 = use --since window)")
	logsCmd.Flags().StringVar(&flagGrep, "grep", "", "Server-side regex line filter (LogQL |~)")
	logsCmd.Flags().BoolVar(&flagJSON, "json", false, "Emit raw NDJSON instead of human-readable lines")
	logsCmd.Flags().BoolVar(&flagNoColor, "no-color", false, "Disable color output")
	logsCmd.Flags().IntVar(&flagLimit, "limit", 0, "Max lines to fetch in range mode (server caps the value; 0 = server default)")
	logsCmd.Flags().StringVarP(&flagService, "service", "s", "", "Filter to a single service (forwarded as ?service=)")
	logsCmd.Flags().BoolVar(&flagPodStream, "pod-stream", false, "Stream pod logs via the K8s API instead of Loki (requires --service)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	alias := args[0]

	if flagPodStream && flagService == "" {
		return fmt.Errorf("--pod-stream requires --service")
	}

	cfg := config.Load()
	if !cfg.HasToken() {
		fmt.Fprintf(os.Stderr, "%s Error: API token is required. Run `dibbla login` or set DIBBLA_API_TOKEN.\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if flagPodStream {
		return runPodStream(ctx, cfg.APIURL, cfg.APIToken, alias)
	}

	body, err := applogs.Stream(ctx, cfg.APIURL, cfg.APIToken, alias, applogs.Options{
		Since:   flagSince,
		Limit:   flagLimit,
		Tail:    flagTail,
		Grep:    flagGrep,
		Follow:  flagFollow,
		Service: flagService,
	})
	if err != nil {
		var httpErr *applogs.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.Status {
			case 401, 403:
				return fmt.Errorf("not authorized — check your API token (got %d)", httpErr.Status)
			case 404:
				return fmt.Errorf("app %q not found in your organization", alias)
			case 503:
				return fmt.Errorf("logs are not enabled on this Dibbla instance: %s", httpErr.Body)
			}
		}
		return err
	}
	defer body.Close()

	useColor := !flagNoColor && !flagJSON && isatty.IsTerminal(os.Stdout.Fd())

	scanner := bufio.NewScanner(body)
	// Allow long log lines (default 64KB is small).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if flagJSON {
			fmt.Println(string(line))
			continue
		}

		entry, ok, derr := applogs.DecodeLine(line)
		if derr != nil {
			fmt.Fprintln(os.Stderr, "logs: "+derr.Error())
			continue
		}
		if !ok {
			// Already handled above (DecodeLine returned an error envelope).
			continue
		}
		fmt.Println(formatEntry(entry, useColor))
	}
	if err := scanner.Err(); err != nil {
		// Cancelled streams produce a context error — exit quietly.
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("read logs stream: %w", err)
	}
	return nil
}

// runPodStream consumes the text/plain pod-log stream from the K8s-direct
// endpoint and copies it to stdout verbatim. The server already prefixes each
// line with `[<pod>] ` so no per-line decoding is needed.
func runPodStream(ctx context.Context, apiURL, apiToken, alias string) error {
	body, err := applogs.StreamPodService(ctx, apiURL, apiToken, alias, flagService, applogs.PodStreamOptions{
		Tail:   flagTail,
		Follow: flagFollow,
	})
	if err != nil {
		var httpErr *applogs.HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.Status {
			case 401, 403:
				return fmt.Errorf("not authorized — check your API token (got %d)", httpErr.Status)
			case 404:
				return fmt.Errorf("no pods for %q/%s — check `dibbla apps get %s`", alias, flagService, alias)
			case 503:
				return fmt.Errorf("kubernetes is not configured on this Dibbla instance: %s", httpErr.Body)
			}
		}
		return err
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("read pod-stream: %w", err)
	}
	return nil
}

// formatEntry renders one entry as `HH:MM:SS.mmm  LEVEL  body`. Level is
// extracted from JSON-shaped lines (slog format) when present; otherwise
// only the timestamp + raw line are printed.
func formatEntry(e applogs.Entry, useColor bool) string {
	ts := e.Timestamp.Local().Format("15:04:05.000")
	level, msg := splitLevelAndMessage(e.Line)

	if level == "" {
		return ts + "  " + e.Line
	}

	tag := padRight(strings.ToUpper(level), 5)
	if useColor {
		tag = colorize(level, tag)
	}
	return ts + "  " + tag + "  " + msg
}

func splitLevelAndMessage(line string) (level, msg string) {
	// Try slog JSON shape first: {"time":"...","level":"INFO","msg":"..."}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var obj struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && obj.Msg != "" {
			return obj.Level, obj.Msg
		}
	}
	return "", line
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func colorize(level, s string) string {
	switch strings.ToUpper(level) {
	case "ERROR", "ERR", "FATAL", "PANIC":
		return "\033[31m" + s + "\033[0m" // red
	case "WARN", "WARNING":
		return "\033[33m" + s + "\033[0m" // yellow
	case "INFO":
		return "\033[36m" + s + "\033[0m" // cyan
	case "DEBUG":
		return "\033[90m" + s + "\033[0m" // bright-black
	}
	return s
}

