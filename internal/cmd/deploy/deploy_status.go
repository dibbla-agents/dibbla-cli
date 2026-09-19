package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/deploy/render"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// `dibbla deploy status <id> [--follow]` (DIB-903): the deploy a `git push`
// to main became. The push answers at once with an operation id — the build
// runs on the platform, not on the push's connection — and this is how the
// pusher watches it. With --follow the ledger is replayed through the same
// renderers `dibbla deploy` uses for a tarball deploy, so the lines and the
// exit code are the ones a deploy would have produced.

var deployStatusCmd = &cobra.Command{
	Use:   "status <operation-id>",
	Short: "Show or follow a deploy started by git push",
	Long: `Show the deploy a git push to main started, or follow it to the end.

A push to main prints the operation id:

  remote: Dibbla: deploying 4f2a9c1e0b7d to myapp
  remote:   operation: deployment:2b1c…
  remote:   follow:    dibbla deploy status deployment:2b1c… --follow

Without --follow the current phase is printed and the command exits 0.
With --follow the build log and rollout are streamed as they happen and the
exit code is the deploy's: 0 when the app is live on the pushed commit,
non-zero when the build or rollout failed (main then still carries the
pushed commit; the running app is unchanged — fix it with the next commit).

Examples:
  dibbla deploy status deployment:2b1c…
  dibbla deploy status deployment:2b1c… --follow
  dibbla deploy status deployment:2b1c… --follow --json`,
	Args: cobra.ExactArgs(1),
	Run:  runDeployStatus,
}

var (
	deployStatusFollow bool
	deployStatusJSON   bool
	deployStatusQuiet  bool
)

func init() {
	deployStatusCmd.Flags().BoolVar(&deployStatusFollow, "follow", false, "Stream events and build log until the deploy finishes; exit with the deploy's code")
	deployStatusCmd.Flags().BoolVar(&deployStatusJSON, "json", false, "Print the operation document (or, with --follow, one structured object on completion)")
	deployStatusCmd.Flags().BoolVar(&deployStatusQuiet, "quiet", false, "With --follow: one line on success/failure")
	deployStatusCmd.MarkFlagsMutuallyExclusive("quiet", "json")
	deployCmd.AddCommand(deployStatusCmd)
}

func runDeployStatus(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	var r render.Renderer
	switch {
	case deployStatusJSON:
		r = render.NewJSON(os.Stdout)
	case deployStatusQuiet:
		r = render.NewQuiet(os.Stdout)
	default:
		// The ledger is a replay, not a live stream with step structure, so
		// the timestamped log renderer is the right shape on a TTY too.
		r = render.NewLog(os.Stdout, os.Stderr)
	}
	os.Exit(runDeployStatusCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], deployStatusFollow, deployStatusJSON, r, 2*time.Second))
}

// runDeployStatusCore is the testable inner implementation. Returns the exit
// code: the deploy's own when following, 0 for a plain status read.
func runDeployStatusCore(stdout, stderr io.Writer, apiURL, apiToken, id string, follow, jsonOut bool, r render.Renderer, poll time.Duration) int {
	id = strings.TrimSpace(id)
	if id == "" {
		fmt.Fprintf(stderr, "%s an operation id is required (it is printed by git push as 'operation: …')\n", platform.Icon("❌", "[X]"))
		return 5
	}
	op, raw, err := apps.GetOperation(apiURL, apiToken, id)
	if err != nil {
		return reportOperationError(stderr, id, err)
	}
	if !follow {
		if jsonOut {
			fmt.Fprintln(stdout, string(raw))
			return 0
		}
		printOperation(stdout, op)
		return 0
	}
	return followOperation(stderr, apiURL, apiToken, op, r, poll)
}

func printOperation(w io.Writer, op *apps.Operation) {
	fmt.Fprintf(w, "%s %s — %s (%s)\n", platform.Icon("🚀", "[DEPLOY]"), op.OperationID, op.Alias, op.Phase)
	if op.Source != nil && op.Source.CommitSHA != "" {
		fmt.Fprintf(w, "   Commit:  %s (%s)\n", op.Source.CommitSHA, op.Source.Ref)
	}
	fmt.Fprintf(w, "   Created: %s\n", op.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	if op.FinishedAt != nil {
		fmt.Fprintf(w, "   Finished: %s\n", op.FinishedAt.Local().Format("2006-01-02 15:04:05"))
	}
	if res := decodeResult(op); res != nil && res.URL != "" {
		fmt.Fprintf(w, "   URL:     %s\n", res.URL)
	}
	if op.Failure != nil {
		fmt.Fprintf(w, "   Failure: %s — %s\n", op.Failure.Code, op.Failure.Summary)
	}
	if !op.Terminal {
		fmt.Fprintf(w, "   follow:  dibbla deploy status %s --follow\n", op.OperationID)
	}
}

func decodeResult(op *apps.Operation) *apps.OperationResult {
	if len(op.Result) == 0 {
		return nil
	}
	var res apps.OperationResult
	if json.Unmarshal(op.Result, &res) != nil {
		return nil
	}
	return &res
}

// followOperation replays the ledger into the renderer until the operation
// is terminal: events as deploy/rollout lines, log lines as build output,
// and the terminal phase as the same result or error event a streamed
// deploy ends with.
func followOperation(stderr io.Writer, apiURL, apiToken string, op *apps.Operation, r render.Renderer, poll time.Duration) int {
	source := op.Alias
	if op.Source != nil && op.Source.CommitSHA != "" {
		source = fmt.Sprintf("%s ← %s (%s)", op.Alias, op.Source.CommitSHA, op.Source.Ref)
	}
	r.OnEvent(render.DeployEvent{Type: "deploy", Ts: op.CreatedAt, Source: source})
	var eventCursor, logCursor int64
	for {
		if err := drainOperation(apiURL, apiToken, op.OperationID, &eventCursor, &logCursor, r); err != nil {
			return reportOperationError(stderr, op.OperationID, err)
		}
		if op.Terminal {
			break
		}
		time.Sleep(poll)
		next, _, err := apps.GetOperation(apiURL, apiToken, op.OperationID)
		if err != nil {
			return reportOperationError(stderr, op.OperationID, err)
		}
		op = next
	}
	r.OnEvent(terminalEvent(op))
	return r.OnDone()
}

func drainOperation(apiURL, apiToken, id string, eventCursor, logCursor *int64, r render.Renderer) error {
	for {
		events, more, err := apps.OperationEvents(apiURL, apiToken, id, *eventCursor)
		if err != nil {
			return err
		}
		for _, ev := range events {
			*eventCursor = ev.Sequence
			r.OnEvent(render.DeployEvent{Type: "deploy", Ts: ev.Timestamp, State: ev.Kind, Source: ev.Text})
		}
		if !more {
			break
		}
	}
	for {
		lines, more, err := apps.OperationLogs(apiURL, apiToken, id, *logCursor)
		if err != nil {
			return err
		}
		for _, l := range lines {
			*logCursor = l.Sequence
			r.OnEvent(render.DeployEvent{Type: "build", Ts: l.Timestamp, State: "log", Log: l.Line})
		}
		if !more {
			return nil
		}
	}
}

func terminalEvent(op *apps.Operation) render.DeployEvent {
	ts := time.Now()
	if op.FinishedAt != nil {
		ts = *op.FinishedAt
	}
	switch op.Phase {
	case "succeeded":
		res := decodeResult(op)
		if res == nil {
			res = &apps.OperationResult{Alias: op.Alias, Status: "success"}
		}
		return render.DeployEvent{Type: "result", Ts: ts, Result: &render.DeployResult{
			Status:     "success",
			Deployment: render.ResultDeployment{ID: res.DeploymentID, Alias: res.Alias, URL: res.URL, Status: res.Status},
			VCSCommit:  res.CommitSHA,
		}}
	case "cancelled":
		return render.DeployEvent{Type: "error", Ts: ts, Error: &render.DeployError{APIError: &render.APIError{Code: "CANCELLED", Message: "the deploy was cancelled"}}}
	default:
		code, msg := "DEPLOY_FAILED", "the deploy failed"
		if op.Failure != nil {
			if op.Failure.Code != "" {
				code = op.Failure.Code
			}
			if op.Failure.Summary != "" {
				msg = op.Failure.Summary
			}
		}
		return render.DeployEvent{Type: "error", Ts: ts, Error: &render.DeployError{APIError: &render.APIError{Code: code, Message: msg}}}
	}
}

func reportOperationError(stderr io.Writer, id string, err error) int {
	var statusErr *apps.StatusError
	if errors.As(err, &statusErr) {
		fmt.Fprintf(stderr, "%s deploy status %s failed: %v\n", platform.Icon("❌", "[X]"), id, statusErr)
		if statusErr.Status == 404 {
			fmt.Fprintln(stderr, "  hint: the id is printed by git push as 'operation: deployment:…' and belongs to the organization you are logged in to.")
		}
		return statusErr.ExitCode()
	}
	fmt.Fprintf(stderr, "%s deploy status %s failed: %v\n", platform.Icon("❌", "[X]"), id, err)
	return 1
}
