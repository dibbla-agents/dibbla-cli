package template

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	cliout "github.com/dibbla-agents/dibbla-cli/internal/output"
	"github.com/dibbla-agents/dibbla-cli/internal/templates"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Discover and install Dibbla templates",
	Long: `Discover and install Dibbla templates.

Templates are listed in a hosted manifest (templates.json) at:
  ` + templates.DefaultManifestURL + `

Override with DIBBLA_TEMPLATES_URL to point at a staging / local manifest.`,
}

// resolveManifest resolves the current manifest, prints a one-line notice to
// stderr when verbose, and returns the manifest. Callers should prefer this
// over calling templates.Resolve directly so the verbose message stays
// consistent across subcommands.
func resolveManifest(refresh, verbose bool) (*templates.Manifest, error) {
	res, err := templates.Resolve(templates.ManifestURL(), refresh)
	if err != nil {
		return nil, fmt.Errorf("%w — check your internet connection", err)
	}
	if verbose {
		cliout.Stderr("templates: %s", sourceMessage(res))
	}
	return res.Manifest, nil
}

func sourceMessage(res *templates.Resolution) string {
	switch res.Source {
	case templates.SourceFreshCache:
		return fmt.Sprintf("from cache, fetched %s ago", humanDuration(res.Age))
	case templates.SourceNetwork:
		return "fresh from network"
	default:
		return "(unknown source)"
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
