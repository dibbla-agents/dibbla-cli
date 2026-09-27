package admincmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/cmd/aigateway"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// `dibbla admin models` edits the platform model catalog in the AI gateway
// (DIB-1114). Unlike reconcile it authenticates as the logged-in user: the
// gateway allows it for global admins only, so there is no shared secret.

const catalogPath = "/console/api/admin/models"

type catalogModel struct {
	Alias                string  `json:"alias"`
	Provider             string  `json:"provider"`
	ProviderModelID      string  `json:"provider_model_id"`
	InputUSDPerMTok      float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok     float64 `json:"output_usd_per_mtok"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok"`
	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok"`
	Active               bool    `json:"active"`
	UpdatedAt            string  `json:"updated_at,omitempty"`
	UpdatedByEmail       string  `json:"updated_by_email,omitempty"`
}

// catalogInput is the PUT body; the gateway refuses any other field.
type catalogInput struct {
	Provider             string  `json:"provider"`
	ProviderModelID      string  `json:"provider_model_id"`
	InputUSDPerMTok      float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok     float64 `json:"output_usd_per_mtok"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok"`
	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok"`
	Active               bool    `json:"active"`
}

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage the platform model catalog (global admins)",
	Long: `Manage the platform model catalog in the Dibbla AI gateway.

Platform workloads name a model as dibbla/<alias>. The catalog says which
provider serves the alias, the provider's model id, and what it costs per
million tokens. The gateway reads the catalog on every call, so a change here
applies to the next call without a deploy.

Requires a global admin login ('dibbla login').`,
}

var modelsJSON bool

var modelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the catalog",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runModelsList(newCatalogClient, os.Stdout, os.Stderr))
	},
}

var setFlags struct {
	provider, model                      string
	input, output, cacheWrite, cacheRead float64
	active                               bool
}

var modelsSetCmd = &cobra.Command{
	Use:   "set <alias>",
	Short: "Create an alias or change one",
	Long: `Create a catalog alias, or change an existing one.

For an existing alias only the flags you pass change; everything else keeps
its current value. A new alias needs --provider, --model and all four prices.
Prices are USD per million tokens.`,
	Example: `  dibbla admin models set sonnet-5 --model claude-sonnet-5-1
  dibbla admin models set sonnet-5 --input 2 --output 10
  dibbla admin models set haiku-4.5 --active=false
  dibbla admin models set opus-next --provider anthropic --model claude-opus-5-5 \
      --input 4 --output 20 --cache-write 5 --cache-read 0.2`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runModelsSet(newCatalogClient, cmd, args[0], os.Stdout, os.Stderr))
	},
}

var modelsDeleteYes bool

var modelsDeleteCmd = &cobra.Command{
	Use:   "delete <alias>",
	Short: "Delete an alias",
	Long: `Delete a catalog alias. Workloads that name it fail until it is added again;
to retire a model without breaking anyone's configuration, prefer
'dibbla admin models set <alias> --active=false'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runModelsDelete(newCatalogClient, args[0], modelsDeleteYes, os.Stdout, os.Stderr))
	},
}

func init() {
	modelsListCmd.Flags().BoolVar(&modelsJSON, "json", false, "Emit JSON")
	f := modelsSetCmd.Flags()
	f.StringVar(&setFlags.provider, "provider", "", "Provider: anthropic or openai")
	f.StringVar(&setFlags.model, "model", "", "The provider's model id, e.g. claude-sonnet-5")
	f.Float64Var(&setFlags.input, "input", 0, "Input price, USD per million tokens")
	f.Float64Var(&setFlags.output, "output", 0, "Output price, USD per million tokens")
	f.Float64Var(&setFlags.cacheWrite, "cache-write", 0, "Cache-write price, USD per million tokens")
	f.Float64Var(&setFlags.cacheRead, "cache-read", 0, "Cache-read price, USD per million tokens")
	f.BoolVar(&setFlags.active, "active", true, "Whether workloads may use the alias")
	modelsDeleteCmd.Flags().BoolVarP(&modelsDeleteYes, "yes", "y", false, "Skip the confirmation prompt")
	modelsCmd.AddCommand(modelsListCmd, modelsSetCmd, modelsDeleteCmd)
}

// catalogClient is the slice of apiclient the commands use; tests fake it.
type catalogClient interface {
	Get(path string) (*apiclient.Response, error)
	Put(path string, body interface{}) (*apiclient.Response, error)
	Delete(path string) (*apiclient.Response, error)
}

func newCatalogClient(stderr io.Writer) (catalogClient, bool) {
	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintf(stderr, "%s not logged in. Run 'dibbla login' first.\n", platform.Icon("❌", "[X]"))
		return nil, false
	}
	base, source := aigateway.GatewayURL()
	if base == "" {
		fmt.Fprintf(stderr, "%s cannot find the AI gateway: %s. Set DIBBLA_AI_GATEWAY_URL.\n", platform.Icon("❌", "[X]"), source)
		return nil, false
	}
	return apiclient.NewClient(base, cfg.APIToken, false), true
}

func reportError(stderr io.Writer, err error) int {
	if apiErr, ok := err.(*apiclient.APIError); ok {
		// The gateway answers {"error":{"code","message"}}; apiclient keeps
		// the raw body, so show the message rather than the JSON.
		msg := strings.TrimSpace(apiErr.Message)
		var env struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(msg), &env) == nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		if apiErr.StatusCode == 403 {
			msg = "the model catalog is for global admins only"
		}
		fmt.Fprintf(stderr, "%s %s\n", platform.Icon("❌", "[X]"), msg)
		return apiclient.ExitCodeForStatus(apiErr.StatusCode)
	}
	fmt.Fprintf(stderr, "%s %v\n", platform.Icon("❌", "[X]"), err)
	return 1
}

func fetchCatalog(c catalogClient) ([]catalogModel, error) {
	resp, err := c.Get(catalogPath)
	if err != nil {
		return nil, err
	}
	var listing struct {
		Models []catalogModel `json:"models"`
	}
	if err := json.Unmarshal(resp.Body, &listing); err != nil {
		return nil, fmt.Errorf("unexpected response from the gateway: %w", err)
	}
	return listing.Models, nil
}

func runModelsList(newClient func(io.Writer) (catalogClient, bool), stdout, stderr io.Writer) int {
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	models, err := fetchCatalog(c)
	if err != nil {
		return reportError(stderr, err)
	}
	if modelsJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(models)
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tPROVIDER\tMODEL\tIN\tOUT\tCACHE WRITE\tCACHE READ\tACTIVE")
	for _, m := range models {
		active := "yes"
		if !m.Active {
			active = "no"
		}
		fmt.Fprintf(w, "dibbla/%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Alias, m.Provider, m.ProviderModelID,
			price(m.InputUSDPerMTok), price(m.OutputUSDPerMTok), price(m.CacheWriteUSDPerMTok), price(m.CacheReadUSDPerMTok), active)
	}
	_ = w.Flush()
	fmt.Fprintln(stdout, "Prices: USD per million tokens.")
	return 0
}

func price(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	if strings.HasSuffix(s, ".") {
		s += "00"
	} else if i := strings.Index(s, "."); i >= 0 && len(s)-i == 2 {
		s += "0"
	}
	return "$" + s
}

func runModelsSet(newClient func(io.Writer) (catalogClient, bool), cmd *cobra.Command, alias string, stdout, stderr io.Writer) int {
	alias = strings.TrimPrefix(strings.TrimSpace(alias), "dibbla/")
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	models, err := fetchCatalog(c)
	if err != nil {
		return reportError(stderr, err)
	}
	var in catalogInput
	existing := false
	for _, m := range models {
		if m.Alias == alias {
			existing = true
			in = catalogInput{m.Provider, m.ProviderModelID, m.InputUSDPerMTok, m.OutputUSDPerMTok, m.CacheWriteUSDPerMTok, m.CacheReadUSDPerMTok, m.Active}
		}
	}
	changed := cmd.Flags().Changed
	if !existing {
		var missing []string
		for _, name := range []string{"provider", "model", "input", "output", "cache-write", "cache-read"} {
			if !changed(name) {
				missing = append(missing, "--"+name)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(stderr, "%s dibbla/%s is a new alias; also pass %s\n", platform.Icon("❌", "[X]"), alias, strings.Join(missing, ", "))
			return 1
		}
		in.Active = true
	}
	if changed("provider") {
		in.Provider = setFlags.provider
	}
	if changed("model") {
		in.ProviderModelID = setFlags.model
	}
	if changed("input") {
		in.InputUSDPerMTok = setFlags.input
	}
	if changed("output") {
		in.OutputUSDPerMTok = setFlags.output
	}
	if changed("cache-write") {
		in.CacheWriteUSDPerMTok = setFlags.cacheWrite
	}
	if changed("cache-read") {
		in.CacheReadUSDPerMTok = setFlags.cacheRead
	}
	if changed("active") {
		in.Active = setFlags.active
	}
	resp, err := c.Put(catalogPath+"/"+url.PathEscape(alias), in)
	if err != nil {
		return reportError(stderr, err)
	}
	var saved catalogModel
	if err := json.Unmarshal(resp.Body, &saved); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	state := "active"
	if !saved.Active {
		state = "inactive"
	}
	fmt.Fprintf(stdout, "%s dibbla/%s → %s %s (%s), in %s, out %s, cache write %s, cache read %s per million tokens\n",
		platform.Icon("✅", "[OK]"), saved.Alias, saved.Provider, saved.ProviderModelID, state,
		price(saved.InputUSDPerMTok), price(saved.OutputUSDPerMTok), price(saved.CacheWriteUSDPerMTok), price(saved.CacheReadUSDPerMTok))
	fmt.Fprintln(stdout, "Applies to the next call.")
	return 0
}

func runModelsDelete(newClient func(io.Writer) (catalogClient, bool), alias string, yes bool, stdout, stderr io.Writer) int {
	alias = strings.TrimPrefix(strings.TrimSpace(alias), "dibbla/")
	if !yes {
		fmt.Fprintf(stderr, "%s deleting dibbla/%s breaks every workload that names it; pass --yes to confirm (or use 'set %s --active=false')\n",
			platform.Icon("❌", "[X]"), alias, alias)
		return 1
	}
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	if _, err := c.Delete(catalogPath + "/" + url.PathEscape(alias)); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "%s dibbla/%s deleted\n", platform.Icon("✅", "[OK]"), alias)
	return 0
}
