package skills

import (
	"github.com/spf13/cobra"

	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
)

var listCmd = &cobra.Command{
	Use:          "list",
	Short:        "List skills bundled with this dibbla version",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runList,
}

func runList(cmd *cobra.Command, args []string) error {
	entries := all()
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.id, e.description})
	}
	cliout.PrintTable([]string{"ID", "DESCRIPTION"}, rows)
	return nil
}
