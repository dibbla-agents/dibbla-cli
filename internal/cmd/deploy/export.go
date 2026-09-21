package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AlecAivazis/survey/v2"
	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/export"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// exportVersion is stamped into dibbla-export.json; set by Register.
var exportVersion = "dev"

var exportCmd = &cobra.Command{
	Use:   "export <alias>",
	Short: "Export an app — source, databases, buckets, environment, manifest — in standard formats",
	Long: `Export everything an app keeps on Dibbla into one directory, in formats other
tools read, so it can be run or hosted somewhere else:

  source/                 git repository of the app's source (every deploy is a commit)
  dibbla.yaml             the app's manifest (from the source, or generated from the deploy)
  databases/<name>.dump   pg_dump custom archives, one per managed database
  buckets/<name>/         every object of each managed bucket, one file per key
  env/app.env             the environment the app reads (secret values blanked)
  docker-compose.yml      a sketch that runs it all locally: Postgres restored from
                          the dumps, MinIO seeded from the buckets
  README.md               what is here and how to start it
  dibbla-export.json      machine-readable inventory

Secret values are not exported unless you pass --include-secrets, which asks
for confirmation (--yes answers it for scripts). The export is read-only: the
app keeps running on Dibbla exactly as before.

Examples:
  dibbla export shop                       # → ./shop-export/
  dibbla export shop --out /tmp/shop       # elsewhere (must be empty or absent)
  dibbla export shop --include-secrets     # with secret values, after confirming`,
	Args: cobra.ExactArgs(1),
	Run:  runExport,
}

var (
	exportOut            string
	exportIncludeSecrets bool
	exportYes            bool
)

func init() {
	exportCmd.Flags().StringVarP(&exportOut, "out", "o", "", "Output directory (default: ./<alias>-export)")
	exportCmd.Flags().BoolVar(&exportIncludeSecrets, "include-secrets", false, "Write secret values into the env files (asks for confirmation)")
	exportCmd.Flags().BoolVarP(&exportYes, "yes", "y", false, "Confirm --include-secrets without asking")
}

func runExport(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runExportCore(os.Stdout, os.Stderr, exportInput{
		Alias:          args[0],
		Out:            exportOut,
		IncludeSecrets: exportIncludeSecrets,
		Yes:            exportYes,
		APIURL:         cfg.APIURL,
		APIToken:       cfg.APIToken,
		Interactive:    isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()),
		Confirm:        confirmIncludeSecrets,
	}))
}

type exportInput struct {
	Alias, Out       string
	IncludeSecrets   bool
	Yes              bool
	APIURL, APIToken string
	Interactive      bool
	// Confirm asks the include-secrets question; default-no, unlike the
	// generic confirm, because the safe answer is the one nobody typed.
	Confirm func() (bool, error)
	// Git overrides the git invocation (tests).
	Git func(args ...string) error
}

func confirmIncludeSecrets() (bool, error) {
	var ok bool
	err := survey.AskOne(&survey.Confirm{
		Message: "Write secret values in plain text into the export?",
		Default: false,
	}, &ok)
	return ok, err
}

// runExportCore is the testable inner implementation. Returns the exit code.
func runExportCore(stdout, stderr io.Writer, in exportInput) int {
	bad := platform.Icon("❌", "[X]")
	if !apps.AliasRe.MatchString(in.Alias) {
		return invalidAlias(stderr, in.Alias)
	}
	out := in.Out
	if out == "" {
		out = in.Alias + "-export"
	}
	if in.IncludeSecrets && !in.Yes {
		if !in.Interactive {
			return refuseUnconfirmable(stderr, "Exporting secret values")
		}
		ok, err := in.Confirm()
		if err != nil {
			if errors.Is(err, prompt.ErrNotInteractive) {
				return refuseUnconfirmable(stderr, "Exporting secret values")
			}
			fmt.Fprintf(stderr, "%s %v\n", bad, err)
			return 1
		}
		if !ok {
			fmt.Fprintln(stdout, "Cancelled: nothing exported. Run without --include-secrets to export with blanked secrets.")
			return 5
		}
	}

	fmt.Fprintf(stdout, "%s Exporting %s → %s\n", platform.Icon("📦", "[>]"), in.Alias, out)
	m, err := export.Run(export.Options{
		APIURL:         in.APIURL,
		APIToken:       in.APIToken,
		Alias:          in.Alias,
		OutDir:         out,
		IncludeSecrets: in.IncludeSecrets,
		CLIVersion:     exportVersion,
		Git:            in.Git,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(stdout, "   "+format+"\n", args...)
		},
	})
	if err != nil {
		code := reportAppError(stderr, "export", in.Alias, err)
		if m != nil {
			fmt.Fprintf(stderr, "  The partial export is left in %s for inspection.\n", out)
		}
		return code
	}

	abs, _ := filepath.Abs(out)
	fmt.Fprintf(stdout, "%s Exported %s to %s\n", platform.Icon("✅", "[OK]"), in.Alias, abs)
	if m.Source != nil {
		fmt.Fprintf(stdout, "   source: %s (%s)\n", m.Source.Path, shortSHA(m.Source.Commit))
	} else {
		fmt.Fprintf(stdout, "   source: not exported — %s\n", m.SourceUnavailable)
	}
	fmt.Fprintf(stdout, "   databases: %d, buckets: %d, env files: %d, manifest: %s (%s)\n",
		len(m.Databases), len(m.Buckets), len(m.Env.Files), m.ManifestFile, m.ManifestOrigin)
	if m.Env.SecretsIncluded {
		fmt.Fprintf(stdout, "   %s secret values are in %s — treat the directory as a password file\n", platform.Icon("⚠️", "[!]"), "env/")
	} else {
		fmt.Fprintln(stdout, "   secret values were not exported; env/ lists the names (--include-secrets to export them)")
	}
	for _, w := range m.Warnings {
		fmt.Fprintf(stdout, "   %s %s\n", platform.Icon("⚠️", "[!]"), w)
	}
	fmt.Fprintln(stdout, "   Start it locally: cd "+out+" && docker compose up --build  (see README.md)")
	return 0
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
