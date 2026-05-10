package aigateway

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var urlVerbose bool

var urlCmd = &cobra.Command{
	Use:   "url",
	Short: "Print the AI gateway base URL",
	Long: `Print the resolved AI gateway base URL on stdout (no trailing newline-friendly
for $(dibbla ai url) interpolation).

Resolution:
  1. DIBBLA_AI_GATEWAY_URL env (if set)
  2. Derived from the active Dibbla API URL (api. → ai.)

Use -v to also print where the value came from.

Exit codes:
  0  resolved
  1  could not resolve (e.g. API URL is localhost) — set DIBBLA_AI_GATEWAY_URL`,
	Args: cobra.NoArgs,
	Run:  runURL,
}

func init() {
	urlCmd.Flags().BoolVarP(&urlVerbose, "verbose", "v", false, "Also print the source of the value to stderr")
}

func runURL(cmd *cobra.Command, args []string) {
	r := resolveGatewayURL()
	if r.URL == "" {
		fmt.Fprintf(os.Stderr, "%s could not resolve gateway URL: %s\n",
			platform.Icon("❌", "[X]"), r.Source)
		fmt.Fprintln(os.Stderr, "    set DIBBLA_AI_GATEWAY_URL or run `dibbla login` against a host with an api. prefix")
		os.Exit(1)
	}
	fmt.Println(r.URL)
	if urlVerbose {
		fmt.Fprintf(os.Stderr, "source: %s\n", r.Source)
	}
}
