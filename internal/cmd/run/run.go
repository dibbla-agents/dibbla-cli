package run

import (
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
	flagPreview  bool
	flagEnv      []string
	flagEnvFile  string
	flagWorkDir  string
	flagFormat   string
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

func runE(cmd *cobra.Command, args []string) error {
	taskPath, isURL, cleanup, err := resolveTaskPath(args)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	workDir, err := resolveWorkDir(isURL)
	if err != nil {
		return err
	}

	env, err := buildEnv()
	if err != nil {
		return err
	}

	formatter, err := pickFormatter()
	if err != nil {
		return err
	}

	if flagPreview {
		preview, err := steprunner.Preview(taskPath)
		if err != nil {
			return err
		}
		printPreview(preview)
		return nil
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := []steprunner.Option{
		steprunner.WithFormatter(formatter),
		steprunner.WithEnv(env),
	}
	if workDir != "" {
		opts = append(opts, steprunner.WithWorkDir(workDir))
	}

	result, err := steprunner.Run(ctx, taskPath, opts...)
	if err != nil {
		return err
	}
	if !result.Success {
		os.Exit(1)
	}
	return nil
}

func resolveTaskPath(args []string) (path string, isURL bool, cleanup func(), err error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}

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

func resolveWorkDir(isURL bool) (string, error) {
	if flagWorkDir != "" {
		return filepath.Abs(flagWorkDir)
	}
	if isURL {
		return os.Getwd()
	}
	return "", nil
}

func buildEnv() (map[string]string, error) {
	env := map[string]string{}

	if flagEnvFile != "" {
		loaded, err := godotenv.Read(flagEnvFile)
		if err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
		for k, v := range loaded {
			env[k] = v
		}
	}

	for _, kv := range flagEnv {
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

func pickFormatter() (model.Formatter, error) {
	switch flagFormat {
	case "plain", "":
		return output.NewPlainFormatter(os.Stdout), nil
	case "gh":
		return output.NewGHActionsFormatter(os.Stdout), nil
	default:
		return nil, fmt.Errorf("unknown --format value %q (want plain or gh)", flagFormat)
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

