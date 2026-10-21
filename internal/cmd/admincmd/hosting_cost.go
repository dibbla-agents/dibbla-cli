package admincmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// `dibbla admin hosting-cost` keeps what this environment's cluster costs a
// month (DIB-1557): the AI gateway's /console/api/admin/hosting-costs. The
// cost view divides it among the apps by what they reserved. Global admins
// only, and only where the cost view is on.

const hostingCostsPath = "/console/api/admin/hosting-costs"

type hostingCost struct {
	ValidFrom  string  `json:"valid_from"`
	ComputeEUR float64 `json:"compute_eur"`
	StorageEUR float64 `json:"storage_eur"`
	Note       string  `json:"note"`
	UpdatedBy  string  `json:"updated_by"`
}

var hostingCostFlags struct {
	from    string
	nodes   float64
	storage float64
	note    string
}

var hostingCostCmd = &cobra.Command{
	Use:   "hosting-cost",
	Short: "What this environment's cluster costs a month (global admins)",
	Long: `What the cluster of this installation costs a month, from a date on: the
nodes, and storage and backups billed apart. 'dibbla admin costs' divides it
among the apps by what each reserved (CPU and memory requests; stored bytes
for the storage part) and shows it as hosting, an estimate.

Each value holds from its date until the next one's. Requires a global admin
login, on an installation where the cost view is on.`,
	Example: `  dibbla admin hosting-cost list
  dibbla admin hosting-cost set --from 2026-10-01 --nodes 372 --storage 31 --note "3 × CCX33 + Storage Box"
  dibbla admin hosting-cost delete --from 2026-10-01`,
}

var hostingCostListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the monthly costs entered",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runHostingCostList(newCatalogClient, os.Stdout, os.Stderr))
	},
}

var hostingCostSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the monthly cost from a date on",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runHostingCostSet(newCatalogClient, os.Stdout, os.Stderr))
	},
}

var hostingCostDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove the monthly cost of a date",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runHostingCostDelete(newCatalogClient, os.Stdout, os.Stderr))
	},
}

func init() {
	hostingCostSetCmd.Flags().StringVar(&hostingCostFlags.from, "from", "", "The date the cost holds from, YYYY-MM-DD (required)")
	hostingCostSetCmd.Flags().Float64Var(&hostingCostFlags.nodes, "nodes", -1, "What the nodes cost a month, in euro (required)")
	hostingCostSetCmd.Flags().Float64Var(&hostingCostFlags.storage, "storage", 0, "What storage and backups billed apart cost a month, in euro")
	hostingCostSetCmd.Flags().StringVar(&hostingCostFlags.note, "note", "", "What the cost is made of")
	hostingCostDeleteCmd.Flags().StringVar(&hostingCostFlags.from, "from", "", "The date of the cost to remove, YYYY-MM-DD (required)")
	hostingCostCmd.AddCommand(hostingCostListCmd, hostingCostSetCmd, hostingCostDeleteCmd)
}

var datePattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`)

func checkFrom(stderr io.Writer) (string, bool) {
	from := strings.TrimSpace(hostingCostFlags.from)
	if !datePattern.MatchString(from) {
		fmt.Fprintf(stderr, "%s --from must be YYYY-MM-DD, got %q\n", platform.Icon("❌", "[X]"), from)
		return "", false
	}
	return from, true
}

func runHostingCostList(newClient func(io.Writer) (catalogClient, bool), stdout, stderr io.Writer) int {
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	resp, err := c.Get(hostingCostsPath)
	if err != nil {
		return reportCostsError(stderr, err)
	}
	var body struct {
		Costs   []hostingCost `json:"costs"`
		InForce string        `json:"in_force"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	if len(body.Costs) == 0 {
		fmt.Fprintln(stdout, "No monthly cost is entered for this environment. Set one with 'dibbla admin hosting-cost set'.")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FROM\tNODES (EUR/MONTH)\tSTORAGE (EUR/MONTH)\tNOTE\tSET BY\t")
	for _, c := range body.Costs {
		mark := ""
		if c.ValidFrom == body.InForce {
			mark = "in force"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ValidFrom, money(c.ComputeEUR, "€"), money(c.StorageEUR, "€"), c.Note, c.UpdatedBy, mark)
	}
	_ = w.Flush()
	return 0
}

func runHostingCostSet(newClient func(io.Writer) (catalogClient, bool), stdout, stderr io.Writer) int {
	from, ok := checkFrom(stderr)
	if !ok {
		return 1
	}
	f := hostingCostFlags
	if f.nodes < 0 || math.IsNaN(f.nodes) || f.storage < 0 || math.IsNaN(f.storage) {
		fmt.Fprintf(stderr, "%s --nodes is required, and --nodes and --storage are euro amounts of 0 or more\n", platform.Icon("❌", "[X]"))
		return 1
	}
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	resp, err := c.Put(hostingCostsPath+"/"+url.PathEscape(from), map[string]any{"compute_eur": f.nodes, "storage_eur": f.storage, "note": f.note})
	if err != nil {
		return reportCostsError(stderr, err)
	}
	var saved hostingCost
	if err := json.Unmarshal(resp.Body, &saved); err != nil {
		return reportError(stderr, fmt.Errorf("unexpected response from the gateway: %w", err))
	}
	fmt.Fprintf(stdout, "%s From %s the cluster costs %s a month for the nodes and %s for storage.\n",
		platform.Icon("✅", "[OK]"), saved.ValidFrom, money(saved.ComputeEUR, "€"), money(saved.StorageEUR, "€"))
	return 0
}

func runHostingCostDelete(newClient func(io.Writer) (catalogClient, bool), stdout, stderr io.Writer) int {
	from, ok := checkFrom(stderr)
	if !ok {
		return 1
	}
	c, ok := newClient(stderr)
	if !ok {
		return 3
	}
	if _, err := c.Delete(hostingCostsPath + "/" + url.PathEscape(from)); err != nil {
		// The view off is a bare 404; a date with no cost is the API's own.
		if apiErr, ok := err.(*apiclient.APIError); ok && apiErr.StatusCode == 404 && strings.Contains(apiErr.Message, "NOT_FOUND") {
			fmt.Fprintf(stderr, "%s no monthly cost is entered from %s\n", platform.Icon("❌", "[X]"), from)
			return 4
		}
		return reportCostsError(stderr, err)
	}
	fmt.Fprintf(stdout, "%s Removed the monthly cost from %s.\n", platform.Icon("✅", "[OK]"), from)
	return 0
}
