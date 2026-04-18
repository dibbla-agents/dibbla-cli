package run

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	steprunner "github.com/dibbla-agents/dibbla-tasks"
	"github.com/dibbla-agents/dibbla-tasks/model"
	"github.com/dibbla-agents/dibbla-tasks/output"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
)

var (
	flagPreview bool
	flagEnv     []string
	flagEnvFile string
	flagWorkDir string
	flagFormat  string
)

var runCmd = &cobra.Command{
	Use:   "run [path-or-url]",
	Short: "Run a dibbla-task.yaml pipeline locally",
	Long: `Run a dibbla-task.yaml pipeline locally on your machine.

The argument is one of:
  (omitted)        runs ./dibbla-task.yaml from the current directory
  <local-path>     runs the given file
  <https-url>      fetches the yaml from the URL and runs it

When the argument is a URL, the working directory defaults to your current
directory (where you invoked dibbla), not the temp dir of the downloaded
yaml. This lets bootstrap yamls clone projects into your CWD.

Security note: dibbla run executes shell commands defined by the task file.
For URL-fetched yamls this is equivalent to "curl | bash" — only run yamls
from sources you trust.`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE:         runE,
}

func init() {
	runCmd.Flags().BoolVar(&flagPreview, "preview", false, "Parse and print the execution plan without running anything")
	runCmd.Flags().StringSliceVar(&flagEnv, "env", nil, "Set an env var for steps (KEY=VAL); repeatable")
	runCmd.Flags().StringVar(&flagEnvFile, "env-file", "", "Load env vars from a .env-style file")
	runCmd.Flags().StringVar(&flagWorkDir, "work-dir", "", "Override working directory for command steps")
	runCmd.Flags().StringVar(&flagFormat, "format", "plain", "Output format: plain | gh")
}

// TaskOpts configures ExecuteTask.
type TaskOpts struct {
	Preview bool
	// Env is a slice of "KEY=VAL" strings, same shape as the --env flag.
	Env []string
	// EnvFile, if non-empty, is loaded via godotenv before applying Env.
	EnvFile string
	// WorkDir overrides the steprunner's work_dir resolution. If empty,
	// URL-sourced tasks default to os.Getwd(); local-file tasks defer to
	// the SDK default (the yaml's parent directory).
	WorkDir string
	// Format is "plain" or "gh". Empty string == "plain".
	Format string
}

// ExecuteTask runs a dibbla-task.yaml given an arg (empty / local path / https URL)
// and options. Exits the process with code 1 when steps fail. Returns a
// non-nil error only for setup failures that should be printed by cobra.
func ExecuteTask(ctx context.Context, arg string, opts TaskOpts) error {
	taskPath, isURL, cleanup, err := resolveTaskPath(arg)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	workDir, err := resolveWorkDir(opts.WorkDir, isURL)
	if err != nil {
		return err
	}

	env, err := buildEnv(opts.Env, opts.EnvFile)
	if err != nil {
		return err
	}

	formatter, err := pickFormatter(opts.Format)
	if err != nil {
		return err
	}

	if opts.Preview {
		preview, err := steprunner.Preview(taskPath)
		if err != nil {
			return err
		}
		printPreview(preview)
		return nil
	}

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	runOpts := []steprunner.Option{
		steprunner.WithFormatter(formatter),
		steprunner.WithEnv(env),
	}
	if workDir != "" {
		runOpts = append(runOpts, steprunner.WithWorkDir(workDir))
	}

	result, err := steprunner.Run(runCtx, taskPath, runOpts...)
	if err != nil {
		return err
	}
	if !result.Success {
		os.Exit(1)
	}
	return nil
}

func runE(cmd *cobra.Command, args []string) error {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	return ExecuteTask(cmd.Context(), arg, TaskOpts{
		Preview: flagPreview,
		Env:     flagEnv,
		EnvFile: flagEnvFile,
		WorkDir: flagWorkDir,
		Format:  flagFormat,
	})
}

func resolveTaskPath(arg string) (path string, isURL bool, cleanup func(), err error) {
	switch {
	case arg == "":
		abs, absErr := filepath.Abs("dibbla-task.yaml")
		if absErr != nil {
			return "", false, nil, absErr
		}
		return abs, false, nil, nil
	case strings.HasPrefix(arg, "https://"):
		path, cleanup, err = FetchYAML(arg)
		return path, true, cleanup, err
	case strings.HasPrefix(arg, "http://"):
		cliout.Stderr("warning: http:// URL — task content becomes shell on your machine; prefer https://")
		path, cleanup, err = FetchYAML(arg)
		return path, true, cleanup, err
	default:
		abs, absErr := filepath.Abs(arg)
		if absErr != nil {
			return "", false, nil, absErr
		}
		return abs, false, nil, nil
	}
}

func resolveWorkDir(override string, isURL bool) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if isURL {
		return os.Getwd()
	}
	return "", nil
}

func buildEnv(envFlags []string, envFile string) (map[string]string, error) {
	env := map[string]string{}

	if envFile != "" {
		loaded, err := godotenv.Read(envFile)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
		for k, v := range loaded {
			env[k] = v
		}
	}

	for _, kv := range envFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env value %q (want KEY=VAL)", kv)
		}
		env[k] = v
	}

	cfg := config.Load()
	if cfg.HasToken() {
		env["DIBBLA_API_TOKEN"] = cfg.APIToken
		env["DIBBLA_AUTH_SERVICE_URL"] = cfg.APIURL
	} else {
		cliout.Stderr("notice: no Dibbla credentials found — steps that need DIBBLA_API_TOKEN will fail")
	}

	if exe, err := os.Executable(); err == nil {
		env["DIBBLA_CMD"] = exe
	}

	return env, nil
}

func pickFormatter(format string) (model.Formatter, error) {
	switch format {
	case "plain", "":
		return output.NewPlainFormatter(os.Stdout), nil
	case "gh":
		return output.NewGHActionsFormatter(os.Stdout), nil
	default:
		return nil, fmt.Errorf("unknown --format value %q (want plain or gh)", format)
	}
}

func printPreview(p *model.PreviewResult) {
	fmt.Println("Execution plan:")
	for i, s := range p.StepSummaries {
		extra := ""
		if s.Tool != "" {
			extra = fmt.Sprintf(" tool=%s", s.Tool)
		}
		if len(s.Platforms) > 0 {
			extra += fmt.Sprintf(" platforms=%s", strings.Join(s.Platforms, ","))
		}
		if s.Mode != "" {
			extra += fmt.Sprintf(" mode=%s", s.Mode)
		}
		fmt.Printf("  %2d. %-22s %-12s %s%s\n", i+1, s.ID, s.Type, s.Name, extra)
	}

	if len(p.EnvVars) > 0 {
		fmt.Println("\nEnv vars:")
		for _, e := range p.EnvVars {
			req := ""
			if e.Required {
				req = " (required)"
			}
			fmt.Printf("  %s%s\n", e.Name, req)
		}
	}

	if len(p.Ports) > 0 {
		fmt.Println("\nPorts:")
		for _, port := range p.Ports {
			fmt.Printf("  %s preferred=%d\n", port.Name, port.Preferred)
		}
	}
}
