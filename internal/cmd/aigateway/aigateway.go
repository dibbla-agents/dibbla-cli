// Package aigateway implements the `dibbla ai` command group:
// helpers for pointing local AI coding assistants (Claude Code, Cursor,
// opencode, …) at the Dibbla AI gateway.
//
// The deployed-app story is solved by `DIBBLA_AI_GATEWAY_URL` injected into
// every pod. On a developer's laptop there is no injected env, so this
// command derives the URL from the active Dibbla API endpoint and prints it
// in the shapes a developer or a script needs:
//
//	dibbla ai url        single line, e.g. https://ai.dibbla.com
//	dibbla ai env        eval-able export block (ANTHROPIC_BASE_URL etc.)
//	dibbla ai test       hits /health on the resolved URL
package aigateway

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
)

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "Helpers for pointing AI assistants (Claude Code, Cursor, opencode, …) at the Dibbla AI gateway",
	Long: `The Dibbla AI gateway proxies OpenAI- and Anthropic-compatible APIs using
your Dibbla API token. Every call is captured and attributed to your user
in the gateway console.

Deployed Dibbla apps get DIBBLA_AI_GATEWAY_URL injected automatically. On
a developer laptop there is no injected env, so these subcommands resolve
the gateway URL from the active Dibbla API endpoint and print it in the
shape your shell, IDE, or curl needs.

Subcommands:
  dibbla ai url     print the gateway base URL
  dibbla ai env     print eval-able exports (ANTHROPIC_BASE_URL, OPENAI_BASE_URL, keys)
  dibbla ai test    hit /health on the resolved gateway`,
}

// Register attaches `dibbla ai` to root.
func Register(root *cobra.Command) {
	aiCmd.AddCommand(urlCmd)
	aiCmd.AddCommand(envCmd)
	aiCmd.AddCommand(testCmd)
	root.AddCommand(aiCmd)
}

// resolveResult captures the gateway URL plus where it came from, so each
// subcommand can show a useful "source" hint without re-deriving.
type resolveResult struct {
	URL    string // gateway base URL, no trailing slash; "" on failure
	Source string // human-readable explanation
	APIURL string // the API URL we derived from (when applicable)
}

// resolveGatewayURL picks a gateway base URL with this precedence:
//
//  1. DIBBLA_AI_GATEWAY_URL env (matches what deployed pods see).
//  2. Derived from the active Dibbla API URL: replace the leading host
//     label "api." with "ai.", drop path/query, keep scheme + port.
//
// Returns URL="" with an explanatory Source when derivation is impossible
// (e.g. APIURL is http://localhost:8090 with no "api." host label).
func resolveGatewayURL() resolveResult {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_AI_GATEWAY_URL")); v != "" {
		return resolveResult{
			URL:    strings.TrimRight(v, "/"),
			Source: "env (DIBBLA_AI_GATEWAY_URL)",
		}
	}

	apiURL := config.Load().APIURL
	derived, err := deriveFromAPIURL(apiURL)
	if err != nil {
		return resolveResult{
			URL:    "",
			Source: fmt.Sprintf("could not derive from API URL %q: %v", apiURL, err),
			APIURL: apiURL,
		}
	}
	return resolveResult{
		URL:    derived,
		Source: fmt.Sprintf("derived from API URL %s (api. → ai.)", apiURL),
		APIURL: apiURL,
	}
}

// deriveFromAPIURL implements the api.X → ai.X host-label rewrite. Path,
// query, and fragment are stripped; scheme and (optional) port are kept.
//
// This is a one-line substitution by design — the platform convention is
// that the gateway lives next to the API on the same parent domain.
// Anything that doesn't match is rejected so the caller can show a clear
// "set DIBBLA_AI_GATEWAY_URL" hint instead of silently producing a wrong URL.
func deriveFromAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("host %q does not start with \"api.\"", host)
	}
	newHost := "ai." + strings.TrimPrefix(host, "api.")
	if port != "" {
		newHost = newHost + ":" + port
	}
	return u.Scheme + "://" + newHost, nil
}
