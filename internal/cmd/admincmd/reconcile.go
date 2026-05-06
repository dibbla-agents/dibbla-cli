package admincmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var reconcileJSON bool

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Force one orphan-resource sweep on the deploy-api instance",
	Long: `Trigger a synchronous orphan-sweep tick on the deploy-api instance.

The reconciler normally runs on a periodic schedule. This command forces one
sweep immediately and prints the names of the K8s objects it deleted (or
would have, depending on operator config).

Authentication:
  Reads DIBBLA_ADMIN_TOKEN from the environment. The user's API token is NOT
  used. The DIBBLA_API_URL (or default) determines which deploy-api instance
  to reach.

Examples:
  DIBBLA_ADMIN_TOKEN=$TOKEN dibbla admin reconcile
  DIBBLA_ADMIN_TOKEN=$TOKEN dibbla admin reconcile --json | jq .`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runReconcile(os.Stdout, os.Stderr))
	},
}

func init() {
	reconcileCmd.Flags().BoolVar(&reconcileJSON, "json", false, "Emit the JSON sweep result instead of human text")
}

// reconcileResult mirrors the deploy-api response shape.
type reconcileResult struct {
	Deployments []string `json:"deployments"`
	Services    []string `json:"services"`
	Ingresses   []string `json:"ingresses"`
}

// runReconcile is the testable entry point. Returns the exit code.
func runReconcile(stdout, stderr io.Writer) int {
	token := strings.TrimSpace(os.Getenv("DIBBLA_ADMIN_TOKEN"))
	if token == "" {
		fmt.Fprintf(stderr, "%s set DIBBLA_ADMIN_TOKEN to run admin commands\n", platform.Icon("❌", "[X]"))
		return 1
	}

	cfg := config.Load()
	url := strings.TrimSuffix(cfg.APIURL, "/") + "/api/deploy/admin/reconcile"

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s create request: %v\n", platform.Icon("❌", "[X]"), err)
		return 1
	}
	req.Header.Set("X-Admin-Token", token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "%s request failed: %v\n", platform.Icon("❌", "[X]"), err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusUnauthorized:
		fmt.Fprintf(stderr, "%s unauthorized; check DIBBLA_ADMIN_TOKEN\n", platform.Icon("❌", "[X]"))
		return 1
	case http.StatusServiceUnavailable:
		fmt.Fprintf(stderr, "%s reconciler not configured on this deploy-api instance\n", platform.Icon("❌", "[X]"))
		return 1
	case http.StatusNotFound:
		fmt.Fprintf(stderr, "%s admin endpoints not enabled on this deploy-api instance\n", platform.Icon("❌", "[X]"))
		return 1
	default:
		fmt.Fprintf(stderr, "%s unexpected status %d: %s\n", platform.Icon("❌", "[X]"), resp.StatusCode, string(body))
		return 1
	}

	var res reconcileResult
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Fprintf(stderr, "%s decode response: %v (body=%s)\n", platform.Icon("❌", "[X]"), err, string(body))
		return 1
	}

	if reconcileJSON {
		_, _ = stdout.Write(body)
		if !strings.HasSuffix(string(body), "\n") {
			_, _ = io.WriteString(stdout, "\n")
		}
		return 0
	}

	fmt.Fprintf(stdout, "%s orphan sweep complete\n", platform.Icon("✓", "[OK]"))
	fmt.Fprintf(stdout, "  deployments: %d", len(res.Deployments))
	if len(res.Deployments) > 0 {
		fmt.Fprintf(stdout, "  (%s)", strings.Join(res.Deployments, ", "))
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  services:    %d", len(res.Services))
	if len(res.Services) > 0 {
		fmt.Fprintf(stdout, "  (%s)", strings.Join(res.Services, ", "))
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  ingresses:   %d", len(res.Ingresses))
	if len(res.Ingresses) > 0 {
		fmt.Fprintf(stdout, "  (%s)", strings.Join(res.Ingresses, ", "))
	}
	fmt.Fprintln(stdout)
	return 0
}
