package aigateway

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	envFormat   string
	envNoExport bool
	envNoToken  bool
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Print eval-able exports for AI assistants (ANTHROPIC_BASE_URL, OPENAI_BASE_URL, …)",
	Long: `Print export lines that point common AI coding assistants at the Dibbla
gateway. Designed for:

    eval $(dibbla ai env)          # bash / zsh
    dibbla ai env --format=fish | source

Variables emitted:
  ANTHROPIC_BASE_URL=<gateway>/anthropic
  ANTHROPIC_API_KEY=<your dibbla token>
  OPENAI_BASE_URL=<gateway>/openai/v1
  OPENAI_API_KEY=<your dibbla token>

Both base URLs use the SAME Dibbla token. The gateway swaps it for the
platform-managed provider key on the way out, so neither you nor the tool
ever sees the upstream key.

Use --no-token to print only the base URLs (handy if you want to keep your
token in a different env file).`,
	Args: cobra.NoArgs,
	Run:  runEnv,
}

func init() {
	envCmd.Flags().StringVar(&envFormat, "format", "shell", "Output format: shell (POSIX), fish, dotenv")
	envCmd.Flags().BoolVar(&envNoExport, "no-export", false, "Omit the leading 'export' (implied for --format=dotenv)")
	envCmd.Flags().BoolVar(&envNoToken, "no-token", false, "Omit ANTHROPIC_API_KEY / OPENAI_API_KEY lines")
}

func runEnv(cmd *cobra.Command, args []string) {
	r := resolveGatewayURL()
	if r.URL == "" {
		fmt.Fprintf(os.Stderr, "%s could not resolve gateway URL: %s\n",
			platform.Icon("❌", "[X]"), r.Source)
		fmt.Fprintln(os.Stderr, "    set DIBBLA_AI_GATEWAY_URL or run `dibbla login` against a host with an api. prefix")
		os.Exit(1)
	}

	cfg := config.Load()
	token := cfg.APIToken
	if !envNoToken && token == "" {
		fmt.Fprintf(os.Stderr, "%s no Dibbla API token configured — run `dibbla login` first, or pass --no-token\n",
			platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	pairs := [][2]string{
		{"ANTHROPIC_BASE_URL", r.URL + "/anthropic"},
		{"OPENAI_BASE_URL", r.URL + "/openai/v1"},
	}
	if !envNoToken {
		pairs = append(pairs,
			[2]string{"ANTHROPIC_API_KEY", token},
			[2]string{"OPENAI_API_KEY", token},
		)
	}

	switch envFormat {
	case "shell", "":
		printShell(pairs, !envNoExport)
	case "fish":
		printFish(pairs)
	case "dotenv":
		printDotenv(pairs)
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q (want shell|fish|dotenv)\n", envFormat)
		os.Exit(2)
	}

	// Comment trailer with the source — visible if the user runs the
	// command interactively but harmless when eval'd.
	fmt.Printf("# gateway: %s\n", r.Source)
}

func printShell(pairs [][2]string, withExport bool) {
	prefix := ""
	if withExport {
		prefix = "export "
	}
	for _, p := range pairs {
		fmt.Printf("%s%s=%s\n", prefix, p[0], shellQuote(p[1]))
	}
}

func printFish(pairs [][2]string) {
	for _, p := range pairs {
		fmt.Printf("set -gx %s %s\n", p[0], shellQuote(p[1]))
	}
}

func printDotenv(pairs [][2]string) {
	for _, p := range pairs {
		fmt.Printf("%s=%s\n", p[0], p[1])
	}
}

// shellQuote wraps a value in single quotes, escaping any embedded single
// quotes — the standard 'safe-for-eval' POSIX form. Tokens won't contain
// quotes in practice, but URLs with weird chars (or a future token format)
// shouldn't be able to break out of the assignment.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
