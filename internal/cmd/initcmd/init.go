package initcmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	flagYes        bool
	flagSkipUpdate bool
	flagSkipSkill  bool
	flagUser       bool
	flagAPIURL     string
	flagReLogin    bool
)

// Runner runs an external command and streams its stdio to the parent.
// Pulled out as an interface so tests can record invocations without
// actually spawning processes.
type Runner interface {
	Run(name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// Override seam for tests. Not exported.
var (
	defaultRunner  Runner = execRunner{}
	executablePath        = os.Executable
	hasToken              = func() bool { return config.Load().HasToken() }
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up dibbla on this machine: update, log in, install the AI-agent skill",
	Long: `One-shot setup wizard. Runs three steps in order using the dibbla binary
itself, so each step picks up the current code:

  1. dibbla update --yes              (skipped on --skip-update)
  2. dibbla login                     (skipped if a token is already configured;
                                       force with --re-login)
  3. dibbla skills install dibbla     (skipped on --skip-skill)

Failure policy:
  - update fails        → warn and continue (CLI is still usable)
  - login fails         → hard stop (everything else needs auth)
  - skills install fails → warn and continue (cosmetic)

Pass --user to install the skill into your home directory instead of the
current project. Pass an existing DIBBLA_API_TOKEN env var to skip the
login prompt entirely (do not pass tokens via flag — they end up in ps).`,
	RunE:         runInit,
	SilenceUsage: true,
}

func init() {
	initCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip prompts where possible (forwarded to update)")
	initCmd.Flags().BoolVar(&flagSkipUpdate, "skip-update", false, "Skip the dibbla update step")
	initCmd.Flags().BoolVar(&flagSkipSkill, "skip-skill", false, "Skip installing the dibbla skill")
	initCmd.Flags().BoolVar(&flagUser, "user", false, "Install the skill into your home directory instead of the current project")
	initCmd.Flags().StringVar(&flagAPIURL, "api-url", "", "API endpoint URL (forwarded to login; e.g. https://api.dibbla.net)")
	initCmd.Flags().BoolVar(&flagReLogin, "re-login", false, "Run login even if a token is already configured")
}

func runInit(cmd *cobra.Command, args []string) error {
	exe, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve dibbla executable: %w", err)
	}
	return orchestrate(cmd, exe, defaultRunner)
}

// orchestrate is the testable core: takes an explicit binary path and
// runner so tests can drive it without exec-ing real subprocesses.
func orchestrate(cmd *cobra.Command, exe string, r Runner) error {
	step := func(n int, total int, label string) {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s Step %d/%d: %s\n",
			platform.Icon("✦", "*"), n, total, label)
	}
	warn := func(stepName string, err error) {
		fmt.Fprintf(cmd.OutOrStdout(),
			"%s %s step failed: %v (continuing)\n",
			platform.Icon("⚠", "[!]"), stepName, err)
	}

	total := 3
	current := 0

	// Step 1: update
	current++
	if flagSkipUpdate {
		step(current, total, "Update — skipped (--skip-update)")
	} else {
		step(current, total, "Updating dibbla to the latest version")
		if err := r.Run(exe, "update", "--yes"); err != nil {
			warn("update", err)
		}
	}

	// Step 2: login (hard-fail)
	current++
	switch {
	case !flagReLogin && hasToken():
		step(current, total, "Login — already configured (skipping; pass --re-login to force)")
	default:
		step(current, total, "Logging in to Dibbla")
		loginArgs := []string{"login"}
		if flagAPIURL != "" {
			loginArgs = append(loginArgs, "--api-url", flagAPIURL)
		}
		if err := r.Run(exe, loginArgs...); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
	}

	// Step 3: skill install
	current++
	if flagSkipSkill {
		step(current, total, "Skill install — skipped (--skip-skill)")
	} else {
		scope := "current project"
		if flagUser {
			scope = "$HOME"
		}
		step(current, total, "Installing dibbla skill into "+scope)
		skillArgs := []string{"skills", "install", "dibbla"}
		if flagUser {
			skillArgs = append(skillArgs, "--user")
		}
		if err := r.Run(exe, skillArgs...); err != nil {
			warn("skills install", err)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\n%s Setup complete. Open your AI code agent in this folder and ask it to build and deploy your app.\n",
		platform.Icon("✓", "[OK]"))
	return nil
}
