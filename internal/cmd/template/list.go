package template

import (
	"github.com/spf13/cobra"

	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
)

var (
	listRefresh bool
	listVerbose bool
)

var listCmd = &cobra.Command{
	Use:          "list",
	Short:        "List available templates",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runList,
}

func init() {
	listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "Force re-fetch of the manifest, bypassing the fresh cache")
	listCmd.Flags().BoolVarP(&listVerbose, "verbose", "v", false, "Print manifest source (cache/network/embedded)")
}

func runList(cmd *cobra.Command, args []string) error {
	m := resolveManifest(listRefresh, listVerbose)
	if m == nil || len(m.Templates) == 0 {
		cliout.Stderr("no templates available")
		return nil
	}

	headers := []string{"ID", "NAME", "CATEGORY", "DESCRIPTION"}
	rows := make([][]string, 0, len(m.Templates))
	for _, t := range m.Templates {
		rows = append(rows, []string{t.ID, t.Name, t.Category, t.Description})
	}
	cliout.PrintTable(headers, rows)
	return nil
}
