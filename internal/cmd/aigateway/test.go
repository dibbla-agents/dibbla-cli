package aigateway

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var testNoToken bool

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Hit /health on the resolved gateway, plus an end-to-end token check",
	Long: `Smoke-test the gateway path.

  1. Resolve the gateway URL (same as 'dibbla ai url').
  2. GET <gateway>/health — confirms TLS + DNS + the gateway pod is up.
  3. Validate the configured Dibbla token against the API — confirms the
     same token your IDE will send is accepted.

If both succeed, your IDE is ready to talk to the gateway with that token.
Use --no-token to skip step 3 (e.g. on a machine that hasn't logged in yet).

Exit codes:
  0  gateway healthy and (unless --no-token) token validated
  1  could not resolve URL, /health failed, or token rejected`,
	Args: cobra.NoArgs,
	Run:  runTest,
}

func init() {
	testCmd.Flags().BoolVar(&testNoToken, "no-token", false, "Skip the token validation step")
}

func runTest(cmd *cobra.Command, args []string) {
	ok := platform.Icon("✅", "[OK]")
	bad := platform.Icon("❌", "[X]")

	r := resolveGatewayURL()
	if r.URL == "" {
		fmt.Fprintf(os.Stderr, "%s could not resolve gateway URL: %s\n", bad, r.Source)
		fmt.Fprintln(os.Stderr, "    set DIBBLA_AI_GATEWAY_URL or run `dibbla login` against a host with an api. prefix")
		os.Exit(1)
	}
	fmt.Printf("Gateway: %s  (%s)\n", r.URL, r.Source)

	if err := pingHealth(r.URL); err != nil {
		fmt.Printf("Health:  %s %v\n", bad, err)
		os.Exit(1)
	}
	fmt.Printf("Health:  %s /health 200\n", ok)

	if testNoToken {
		fmt.Printf("Token:   skipped (--no-token)\n")
		return
	}

	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Printf("Token:   %s no Dibbla API token configured — run `dibbla login`\n", bad)
		os.Exit(1)
	}
	if err := apiclient.ValidateToken(cfg.APIURL, cfg.APIToken); err != nil {
		fmt.Printf("Token:   %s rejected by %s: %v\n", bad, cfg.APIURL, err)
		os.Exit(1)
	}
	fmt.Printf("Token:   %s accepted by %s\n", ok, cfg.APIURL)
}

func pingHealth(base string) error {
	req, err := http.NewRequest("GET", base+"/health", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
