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
	Long: `Values live in Dibbla, names live in the code. Variables and secrets are
injected into the app when it runs; .env.example in the repository lists the
names the app needs. 'dibbla env pull' fetches the variables' values and the
secrets' names to a local .env.local so the app can run here. A secret's value
never leaves Dibbla: you set your own development value for each. The file
never goes back: .gitignore, the VCS filter and the push hook all refuse it.`,
}

var envPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Write the app's environment to .env.local for running it locally",
	Long: `Fetch the app's environment, resolved exactly as the running app gets it
(global, then per app, then per service with --service), and write it to
.env.local in the current folder: every variable with its value, and every
secret by name.

Secrets are write-only. Dibbla never hands out a secret's value — not to this
command, not to an API, not to an AI assistant — so a secret arrives in
.env.local as an empty NAME= line. Fill in a development value of your own (a
test key, a local database). A secret line that already has a value is yours
and is never overwritten, not even by --replace. For the app's database,
'dibbla db connect <name>' gives you a connection of your own.

Values live in Dibbla, names live in the code: keep the names in .env.example
(committed), set a new value with 'dibbla secrets set', and run this again.
The file lives only on this machine; .gitignore gets a .env.local line if it
lacks one, and the VCS filter and the push hook refuse the file on top of that.

The app is the one this folder is linked to (dibbla clone / dibbla link), or
--deployment <alias>. An existing .env.local is updated in place: variables
that exist in Dibbla are refreshed, everything else you put there stays;
--replace rewrites the file, keeping only the secrets you filled in.

Reading the environment needs the deploy roles (owner, admin, developer); a
viewer is refused.

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
			fmt.Fprintln(stderr, "  Reading the environment needs the deploy roles (owner, admin, developer).")
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
	secretNames := doc.Secrets
	sort.Slice(secretNames, func(i, j int) bool { return secretNames[i].Name < secretNames[j].Name })
	if in.Stdout {
		for _, v := range vars {
			fmt.Fprintln(stdout, envfile.FormatLine(v.Name, v.Value))
		}
		for _, sec := range secretNames {
			fmt.Fprintf(stdout, "# %s is a secret: Dibbla never hands out its value\n", sec.Name)
		}
		return 0
	}

	path := filepath.Join(in.Dir, envLocalFile)
	kept, filled, err := writeEnvLocal(path, vars, secretNames, in.Replace)
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
	if n := len(secretNames); n > 0 {
		fmt.Fprintf(stdout, "   %d secret(s) by name only — Dibbla never hands out a secret's value; fill in development values (%d already set here)\n", n, len(filled))
	}
	var suspects []string
	for _, v := range vars {
		if v.LooksLikeSecret {
			suspects = append(suspects, v.Name)
		}
	}
	if len(suspects) > 0 {
		fmt.Fprintf(stdout, "   %s look like secrets but are plain env vars, readable by anyone who can read the app's configuration. Make them secrets: 'dibbla secrets set NAME -d %s', then drop them from -e / dibbla.yaml.\n", strings.Join(suspects, ", "), alias)
	}
	if len(filled) > 0 {
		fmt.Fprintf(stdout, "   kept your local values for %s. If they came from a pull before secrets were write-only, they are the app's real secrets: replace them with development values.\n", strings.Join(filled, ", "))
	}
	if kept > 0 {
		fmt.Fprintf(stdout, "   kept %d local line(s) that Dibbla does not know about (--replace rewrites the file)\n", kept)
	}
	if added {
		fmt.Fprintf(stdout, "   added %s to .gitignore so git never sees it\n", envLocalFile)
	}
	fmt.Fprintln(stdout, "   This file lives only on this machine.")
	return 0
}

func serviceSuffix(service string) string {
	if service == "" {
		return ""
	}
	return ", service " + service
}

// summarizeSources renders "3 inline, 2 platform" — names and counts only,
// never values.
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

// secretsBlockHeader introduces the secret lines env pull appends. Recognised
// on re-pull so it is written once.
const secretsBlockHeader = "# Secrets: Dibbla never hands out a secret's value. Set a development value for each one here."

// secretHint is the comment written above a secret's empty line when there is
// something more specific to say than the block header.
func secretHint(name string) string {
	if db, ok := strings.CutPrefix(name, "DATABASE_URL_"); ok && db != "" {
		// The hint never suggests printing the URL: it carries the person's
		// own API token, so it belongs in a start command, not in this file.
		return fmt.Sprintf("# %s: for a connection of your own, use \"$(dibbla db connect %s -q)\" in the start command — it carries your API token, so not in this file", name, strings.ToLower(db))
	}
	return ""
}

// writeEnvLocal writes the pulled environment to path.
//
// Variables carry their values. With replace, the file is the header, the
// variables and the secrets and nothing else. Otherwise an existing file is
// updated in place — variables Dibbla knows are refreshed where they stand,
// every other line (a local override, a comment) survives, new variables are
// appended — and the header is put on top if it is missing.
//
// Secrets come by name only (DIB-1337). A secret's line that is already in the
// file is the developer's own and is never touched, not even by replace; a
// missing one is appended as an empty NAME= line under secretsBlockHeader.
//
// Returns the number of lines kept that Dibbla did not decide (secret lines
// excluded) and the names of the secrets that already had a local value.
func writeEnvLocal(path string, vars []apps.EnvVariable, secrets []apps.EnvSecret, replace bool) (kept int, filled []string, err error) {
	updates := make(map[string]string, len(vars))
	for _, v := range vars {
		updates[v.Name] = v.Value
	}
	isSecret := make(map[string]bool, len(secrets))
	for _, sec := range secrets {
		isSecret[sec.Name] = true
		// A secret's name is never written as a variable, even if a variable
		// of the same name came back too.
		delete(updates, sec.Name)
	}

	existing, rerr := os.ReadFile(path)
	if rerr != nil && !os.IsNotExist(rerr) {
		return 0, nil, fmt.Errorf("read %s: %w", path, rerr)
	}
	localSecret := map[string]string{} // secret name → its line as it stands
	hasHeader, hasSecretsHeader := false, false
	for _, line := range strings.Split(string(existing), "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case envHeader:
			hasHeader = true
			continue
		case secretsBlockHeader:
			hasSecretsHeader = true
			continue
		case "":
			continue
		}
		if key, ok := envfile.ParseKey(line); ok {
			if isSecret[key] {
				if _, seen := localSecret[key]; !seen {
					localSecret[key] = line
					if hasLocalValue(line) {
						filled = append(filled, key)
					}
				}
				continue
			}
			if _, fromDibbla := updates[key]; fromDibbla {
				continue
			}
		}
		if strings.HasPrefix(trimmed, "# DATABASE_URL_") && strings.Contains(trimmed, "dibbla db connect") {
			continue // a hint this command wrote
		}
		kept++
	}
	sort.Strings(filled)

	if replace {
		var b strings.Builder
		b.WriteString(envHeader + "\n")
		names := make([]string, 0, len(updates))
		for name := range updates {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			b.WriteString(envfile.FormatLine(name, updates[name]) + "\n")
		}
		writeSecretsBlock(&b, secrets, localSecret)
		return 0, filled, envfile.WriteFile(path, []byte(b.String()))
	}

	if len(existing) == 0 || !hasHeader {
		// Header first: a file that starts with what it is, before any value.
		body := string(existing)
		if len(body) > 0 && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := envfile.WriteFile(path, []byte(envHeader+"\n"+body)); err != nil {
			return 0, nil, err
		}
	}
	if _, err := envfile.MergeEnvFile(path, updates); err != nil {
		return 0, nil, err
	}

	// Append the secrets the file does not name yet; the ones it does are
	// left exactly as they are.
	var missing []apps.EnvSecret
	for _, sec := range secrets {
		if _, ok := localSecret[sec.Name]; !ok {
			missing = append(missing, sec)
		}
	}
	if len(missing) == 0 {
		return kept, filled, nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("read %s: %w", path, err)
	}
	var b strings.Builder
	b.Write(current)
	if len(current) > 0 && !strings.HasSuffix(string(current), "\n") {
		b.WriteString("\n")
	}
	if hasSecretsHeader {
		writeSecretLines(&b, missing, nil)
	} else {
		writeSecretsBlock(&b, missing, nil)
	}
	return kept, filled, envfile.WriteFile(path, []byte(b.String()))
}

// writeSecretsBlock writes the secrets header and one line per secret: the
// developer's own line where there is one, an empty NAME= line otherwise.
func writeSecretsBlock(b *strings.Builder, secrets []apps.EnvSecret, local map[string]string) {
	if len(secrets) == 0 {
		return
	}
	b.WriteString("\n" + secretsBlockHeader + "\n")
	writeSecretLines(b, secrets, local)
}

func writeSecretLines(b *strings.Builder, secrets []apps.EnvSecret, local map[string]string) {
	for _, sec := range secrets {
		if line, ok := local[sec.Name]; ok {
			b.WriteString(line + "\n")
			continue
		}
		if hint := secretHint(sec.Name); hint != "" {
			b.WriteString(hint + "\n")
		}
		b.WriteString(sec.Name + "=\n")
	}
}

// hasLocalValue reports whether an env line assigns a non-empty value.
func hasLocalValue(line string) bool {
	_, v, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v != ""
}
