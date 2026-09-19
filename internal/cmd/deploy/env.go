package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	envfile "github.com/dibbla-agents/dibbla-cli/internal/env"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// DIB-919: values live in Dibbla, names live in the code. `.env.example` in
// the repository lists what the app needs; `dibbla env pull` fetches the
// values into `.env.local` on this machine; `.gitignore`, the VCS filter and
// the pre-receive hook keep that file from ever travelling back.

// envLocalFile is the file env pull writes. Not `.env`: the CLI itself reads
// `./.env` for DIBBLA_API_TOKEN, and the app's own environment must not be
// mistaken for the CLI's.
const envLocalFile = ".env.local"

// envHeader is the first line of a pulled file: what it is, where it lives,
// how to refresh it. Recognised on re-pull so it is written once.
const envHeader = "# Pulled from Dibbla — lives only on this machine; run 'dibbla env pull' again to refresh."

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "The app's environment on this machine (values live in Dibbla, names in the code)",
	Long: `Values live in Dibbla, names live in the code. Secrets and the variables the
platform generates (DATABASE_URL_*, STORAGE_*, DIBBLA_*) are injected into the
app when it runs; .env.example in the repository lists the names the app
needs. 'dibbla env pull' fetches the values to a local .env.local so the app
can run here — that file never goes back: .gitignore, the VCS filter and the
push hook all refuse it.`,
}

var envPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Write the app's environment to .env.local for running it locally",
	Long: `Fetch the app's environment — its secrets, resolved exactly as the running
app gets them (global, then per app, then per service with --service), plus
the platform-generated variables it sees (DATABASE_URL_*, STORAGE_*,
DIBBLA_*) — and write it to .env.local in the current folder.

Values live in Dibbla, names live in the code: keep the names in .env.example
(committed), set a new value with 'dibbla secrets set', and run this again.
The file lives only on this machine; .gitignore gets a .env.local line if it
lacks one, and the VCS filter and the push hook refuse the file on top of that.

The app is the one this folder is linked to (dibbla clone / dibbla link), or
--deployment <alias>. An existing .env.local is updated in place: keys that
exist in Dibbla are refreshed, everything else you put there stays; --replace
rewrites the whole file.

A local run with this file talks to the app's real database and buckets —
Dibbla has one environment per app. Say so before starting the app, and offer
a local Postgres if the person wants to work in isolation.

Reading values needs the deploy roles (owner, admin, developer), the same rule
as 'dibbla secrets get'; a viewer is refused.

Examples:
  dibbla env pull                          # linked folder → .env.local
  dibbla env pull -d shop --service worker # one service's view of a multi-service app
  dibbla env pull --replace                # rewrite .env.local from scratch
  eval "$(dibbla env pull --stdout)"       # into the current shell instead of a file
  dibbla env pull --json                   # names, values and which layer each came from`,
	Args: cobra.NoArgs,
	Run:  runEnvPull,
}

var (
	envPullDeployment string
	envPullService    string
	envPullReplace    bool
	envPullStdout     bool
	envPullJSON       bool
)

func init() {
	envCmd.AddCommand(envPullCmd)
	envPullCmd.Flags().StringVarP(&envPullDeployment, "deployment", "d", "", "App alias (default: the app this folder is linked to)")
	envPullCmd.Flags().StringVarP(&envPullService, "service", "s", "", "Resolve the environment of one service of a multi-service app")
	envPullCmd.Flags().BoolVar(&envPullReplace, "replace", false, "Rewrite .env.local from scratch instead of updating it in place")
	envPullCmd.Flags().BoolVar(&envPullStdout, "stdout", false, "Print KEY=value lines to stdout instead of writing .env.local")
	envPullCmd.Flags().BoolVar(&envPullJSON, "json", false, "Print the raw API document (names, values, sources) instead of writing .env.local")
}

func runEnvPull(cmd *cobra.Command, args []string) {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runEnvPullCore(os.Stdout, os.Stderr, envPullInput{
		Dir:        dir,
		Deployment: envPullDeployment,
		Service:    envPullService,
		Replace:    envPullReplace,
		Stdout:     envPullStdout,
		JSON:       envPullJSON,
		APIURL:     cfg.APIURL,
		APIToken:   cfg.APIToken,
	}))
}

type envPullInput struct {
	Dir                 string
	Deployment, Service string
	Replace             bool
	Stdout, JSON        bool
	APIURL, APIToken    string
}

// runEnvPullCore is the testable inner implementation of `env pull`. Returns
// the exit code. It never prints a value to stdout or stderr except in the
// two modes that exist for that (--stdout, --json).
func runEnvPullCore(stdout, stderr io.Writer, in envPullInput) int {
	bad := platform.Icon("❌", "[X]")
	if in.Stdout && in.JSON {
		fmt.Fprintf(stderr, "%s --stdout and --json are two output formats; pick one.\n", bad)
		return 5
	}
	alias := in.Deployment
	if alias == "" {
		_, _, target, ok := linkedFolder(in.Dir)
		if !ok {
			fmt.Fprintf(stderr, "%s this folder is not linked to an app.\n", bad)
			fmt.Fprintln(stderr, "  hint: run it in a folder from 'dibbla clone <app>', or name the app with --deployment <alias>.")
			return 5
		}
		alias = target.App
	}
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}

	doc, raw, err := apps.GetEnv(in.APIURL, in.APIToken, alias, in.Service)
	if err != nil {
		var statusErr *apps.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == 403 && statusErr.Code == "ROLE_FORBIDDEN" {
			fmt.Fprintf(stderr, "%s env pull %s refused: %s\n", bad, alias, statusErr.Message)
			fmt.Fprintln(stderr, "  Reading values needs the deploy roles (owner, admin, developer) — the same rule as 'dibbla secrets get'.")
			return statusErr.ExitCode()
		}
		return reportAppError(stderr, "env pull", alias, err)
	}
	if in.JSON {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	vars := doc.Variables
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
	if in.Stdout {
		for _, v := range vars {
			fmt.Fprintln(stdout, envfile.FormatLine(v.Name, v.Value))
		}
		return 0
	}

	path := filepath.Join(in.Dir, envLocalFile)
	written, kept, err := writeEnvLocal(path, vars, in.Replace)
	if err != nil {
		fmt.Fprintf(stderr, "%s %v\n", bad, err)
		return 1
	}
	gitignore := filepath.Join(in.Dir, ".gitignore")
	added, gerr := envfile.EnsureGitignoreLine(gitignore, envLocalFile)
	if gerr != nil {
		fmt.Fprintf(stderr, "%s %s is written but .gitignore could not be updated: %v\n", platform.Icon("⚠️", "[!]"), envLocalFile, gerr)
		fmt.Fprintf(stderr, "  Add a line '%s' to %s yourself before committing anything.\n", envLocalFile, gitignore)
	}

	fmt.Fprintf(stdout, "%s %s: %d variable(s) from Dibbla (app %s%s) — %s\n",
		platform.Icon("✅", "[OK]"), envLocalFile, len(vars), alias, serviceSuffix(doc.Service), summarizeSources(vars))
	if kept > 0 {
		fmt.Fprintf(stdout, "   kept %d local line(s) that Dibbla does not know about (--replace rewrites the file)\n", kept)
	}
	if added {
		fmt.Fprintf(stdout, "   added %s to .gitignore so git never sees it\n", envLocalFile)
	}
	fmt.Fprintln(stdout, "   This file lives only on this machine. A local run with it uses the app's real database and buckets.")
	_ = written
	return 0
}

func serviceSuffix(service string) string {
	if service == "" {
		return ""
	}
	return ", service " + service
}

// summarizeSources renders "3 global, 7 app, 2 platform" — names and counts
// only, never values.
func summarizeSources(vars []apps.EnvVariable) string {
	counts := map[string]int{}
	for _, v := range vars {
		counts[v.Source]++
	}
	var parts []string
	for _, s := range []struct{ key, label string }{
		{"global", "global"}, {"deployment", "app"}, {"service", "service"}, {"inline", "inline"}, {"platform", "platform"},
	} {
		if n := counts[s.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s.label))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// writeEnvLocal writes vars to path. With replace, the file is the header and
// the variables and nothing else. Otherwise an existing file is updated in
// place — keys Dibbla knows are refreshed where they stand, every other line
// (a local override, a comment) survives, new keys are appended — and the
// header is put on top if it is missing. Returns the keys written and the
// number of lines kept that Dibbla did not decide.
func writeEnvLocal(path string, vars []apps.EnvVariable, replace bool) (written []string, kept int, err error) {
	updates := make(map[string]string, len(vars))
	for _, v := range vars {
		updates[v.Name] = v.Value
	}
	if replace {
		var b strings.Builder
		b.WriteString(envHeader + "\n")
		for _, v := range vars {
			b.WriteString(envfile.FormatLine(v.Name, v.Value) + "\n")
			written = append(written, v.Name)
		}
		return written, 0, envfile.WriteFile(path, []byte(b.String()))
	}
	existing, rerr := os.ReadFile(path)
	if rerr != nil && !os.IsNotExist(rerr) {
		return nil, 0, fmt.Errorf("read %s: %w", path, rerr)
	}
	hasHeader := false
	for _, line := range strings.Split(string(existing), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == envHeader {
			hasHeader = true
			continue
		}
		if trimmed == "" {
			continue
		}
		if key, ok := envfile.ParseKey(line); ok {
			if _, fromDibbla := updates[key]; fromDibbla {
				continue
			}
		}
		kept++
	}
	if len(existing) == 0 || !hasHeader {
		// Header first: a file that starts with what it is, before any value.
		body := string(existing)
		if len(body) > 0 && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := envfile.WriteFile(path, []byte(envHeader+"\n"+body)); err != nil {
			return nil, 0, err
		}
	}
	written, err = envfile.MergeEnvFile(path, updates)
	return written, kept, err
}
