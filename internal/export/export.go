// Package export assembles a self-contained, standard-format copy of one app
// — source, databases, buckets, environment and manifest — so a customer can
// leave the platform with everything they put on it (DIB-979, EU Data Act
// chapter VI). Every artefact is in a format some other tool already reads:
// a git repository, pg_dump custom archives, plain object files, dotenv
// files, the app's own dibbla.yaml, and a docker compose sketch that runs
// them together.
package export

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/db"
	envfile "github.com/dibbla-agents/dibbla-cli/internal/env"
	"github.com/dibbla-agents/dibbla-cli/internal/gitcred"
	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
	"github.com/dibbla-agents/dibbla-cli/internal/storage"
	"github.com/dibbla-agents/dibbla-cli/internal/vcs"
)

// Format names the layout written to the output directory; bump it when
// a file moves or changes shape.
const Format = "dibbla-export/v1"

// Options drives one export.
type Options struct {
	APIURL   string
	APIToken string
	Alias    string
	// OutDir receives the export. It must not exist or must be empty.
	OutDir string
	// IncludeSecrets writes secret values into the env files. The caller
	// owns the confirmation; the package only does what it is told.
	IncludeSecrets bool
	// CLIVersion is recorded in the manifest.
	CLIVersion string
	// Logf receives progress lines. Nil means silent.
	Logf func(format string, args ...any)

	// Git overrides the git invocation (tests). Nil runs the real binary.
	Git func(args ...string) error
}

// Manifest is dibbla-export.json: what was exported, where each piece sits
// and what was deliberately left out.
type Manifest struct {
	Format     string    `json:"format"`
	ExportedAt time.Time `json:"exported_at"`
	CLIVersion string    `json:"cli_version,omitempty"`
	App        AppInfo   `json:"app"`
	Source     *Source   `json:"source,omitempty"`
	// SourceUnavailable says why Source is nil.
	SourceUnavailable string     `json:"source_unavailable,omitempty"`
	ManifestFile      string     `json:"manifest_file"`
	ManifestOrigin    string     `json:"manifest_origin"` // "source" | "generated"
	Databases         []Database `json:"databases"`
	Buckets           []Bucket   `json:"buckets"`
	Env               Env        `json:"env"`
	Warnings          []string   `json:"warnings,omitempty"`
}

type AppInfo struct {
	Alias    string        `json:"alias"`
	URL      string        `json:"url,omitempty"`
	Port     *int          `json:"port,omitempty"`
	Replicas *int          `json:"replicas,omitempty"`
	CPU      string        `json:"cpu,omitempty"`
	Memory   string        `json:"memory,omitempty"`
	Services []ServiceInfo `json:"services,omitempty"`
}

type ServiceInfo struct {
	Name     string `json:"name"`
	Image    string `json:"image,omitempty"`
	Port     *int   `json:"port,omitempty"`
	Replicas int    `json:"replicas"`
	IsPublic bool   `json:"is_public"`
	IsBuilt  bool   `json:"is_built"`
	Stateful bool   `json:"stateful,omitempty"`
}

type Source struct {
	Path     string `json:"path"`
	Commit   string `json:"commit,omitempty"`
	CloneURL string `json:"clone_url,omitempty"`
}

type Database struct {
	Name      string `json:"name"`
	File      string `json:"file"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
}

type Bucket struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Objects int      `json:"objects"`
	Bytes   int64    `json:"bytes"`
	Skipped []string `json:"skipped_keys,omitempty"`
}

type Env struct {
	Files           []string      `json:"files"`
	SecretsIncluded bool          `json:"secrets_included"`
	Variables       []EnvVariable `json:"variables"`
}

// EnvVariable is a name and where it came from — never a value; values live
// only in the env files.
type EnvVariable struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Service string `json:"service,omitempty"`
	File    string `json:"file"`
}

// Run performs the export and returns its manifest. The output directory is
// left in place on error so a partial export can be inspected; the caller
// decides whether to remove it.
func Run(opts Options) (*Manifest, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Alias == "" {
		return nil, errors.New("app alias is required")
	}
	if err := prepareOutDir(opts.OutDir); err != nil {
		return nil, err
	}

	app, _, err := apps.GetApp(opts.APIURL, opts.APIToken, opts.Alias)
	if err != nil {
		return nil, err
	}

	m := &Manifest{
		Format:     Format,
		ExportedAt: time.Now().UTC(),
		CLIVersion: opts.CLIVersion,
		App:        appInfo(app),
		Databases:  []Database{},
		Buckets:    []Bucket{},
	}

	// 1. Source.
	logf("Source")
	if err := exportSource(opts, m); err != nil {
		return m, err
	}

	// 2. Databases.
	logf("Databases")
	if err := exportDatabases(opts, m, logf); err != nil {
		return m, err
	}

	// 3. Buckets.
	logf("Buckets")
	if err := exportBuckets(opts, m, logf); err != nil {
		return m, err
	}

	// 4. Environment.
	logf("Environment")
	envFiles, err := exportEnv(opts, app, m, logf)
	if err != nil {
		return m, err
	}

	// 5. dibbla.yaml, docker compose sketch, README.
	if err := exportManifestFile(opts, m); err != nil {
		return m, err
	}
	services := composeServices(m, loadSourceManifest(opts.OutDir, m))
	if err := writeFile(filepath.Join(opts.OutDir, "docker-compose.yml"), []byte(composeFile(m, envFiles, services))); err != nil {
		return m, err
	}
	if len(m.Databases) > 0 {
		if err := writeFile(filepath.Join(opts.OutDir, "compose", "restore-databases.sh"), []byte(restoreScript(m))); err != nil {
			return m, err
		}
	}
	if err := writeFile(filepath.Join(opts.OutDir, "README.md"), []byte(readme(m, opts.IncludeSecrets))); err != nil {
		return m, err
	}

	// 6. The manifest last, so its presence means the export is complete.
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	if err := writeFile(filepath.Join(opts.OutDir, "dibbla-export.json"), append(data, '\n')); err != nil {
		return m, err
	}
	return m, nil
}

func prepareOutDir(dir string) error {
	if dir == "" {
		return errors.New("output directory is required")
	}
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return fmt.Errorf("read %s: %w", dir, err)
	case len(entries) > 0:
		return fmt.Errorf("%s exists and is not empty; pick another --out or empty it first", dir)
	}
	return nil
}

func appInfo(app *apps.Deployment) AppInfo {
	info := AppInfo{Alias: app.Alias, URL: app.URL, Port: app.Port, Replicas: app.Replicas, CPU: app.CPU, Memory: app.Memory}
	for _, s := range app.Services {
		info.Services = append(info.Services, ServiceInfo{
			Name: s.Name, Image: s.Image, Port: s.Port, Replicas: s.Replicas,
			IsPublic: s.IsPublic, IsBuilt: s.IsBuilt, Stateful: s.Stateful,
		})
	}
	return info
}

// exportSource clones the app's Dibbla-managed repository into source/. An
// app without version control (feature off, or never deployed through it)
// is recorded, not failed: the rest of the export is still worth having.
func exportSource(opts Options, m *Manifest) error {
	info, err := vcs.GetInfo(opts.APIURL, opts.APIToken, opts.Alias)
	if err != nil {
		var apiErr *vcs.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			m.SourceUnavailable = "version control is not enabled for this app"
			return nil
		}
		return fmt.Errorf("source: %w", err)
	}
	if info.CloneURL == "" {
		m.SourceUnavailable = "version control is not enabled for this environment"
		return nil
	}
	if info.LatestSHA == "" {
		m.SourceUnavailable = "the app has no deploy-written commits"
		return nil
	}
	dest := filepath.Join(opts.OutDir, "source")
	git := opts.Git
	if git == nil {
		// git authenticates through the credential helper `dibbla login`
		// registers for Dibbla's git host — the token never lands in the
		// URL, .git/config or the process arg list. Same path as `dibbla
		// clone`; registration is idempotent.
		if err := gitcred.RegisterGitHost(info.CloneURL, opts.APIURL); err != nil {
			return fmt.Errorf("register git credential helper: %w", err)
		}
		git = runGit
	}
	if err := git("clone", "--quiet", info.CloneURL, dest); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	m.Source = &Source{Path: "source", Commit: info.LatestSHA, CloneURL: info.CloneURL}
	return nil
}

func runGit(args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is required on PATH to export the source")
	}
	cmd := exec.Command("git", args...)
	// Never stall on a password prompt: the credential helper answers for
	// Dibbla, and anything else should fail so the caller can say why.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func exportDatabases(opts Options, m *Manifest, logf func(string, ...any)) error {
	info, err := db.ListDatabasesInfo(opts.APIURL, opts.APIToken)
	if err != nil {
		if isNotConfigured(err) {
			m.Warnings = append(m.Warnings, "managed databases are not available in this environment")
			return nil
		}
		return fmt.Errorf("databases: %w", err)
	}
	for _, d := range info.Databases {
		if d.DeploymentAlias != opts.Alias {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("databases", d.Name+".dump"))
		path := filepath.Join(opts.OutDir, "databases", d.Name+".dump")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) // readable by the postgres user in the compose container
		if err != nil {
			return err
		}
		logf("  %s → %s", d.Name, rel)
		if err := db.DumpDatabase(opts.APIURL, opts.APIToken, d.Name, f); err != nil {
			f.Close()
			return fmt.Errorf("dump %s: %w", d.Name, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
		st, _ := os.Stat(path)
		var size int64
		if st != nil {
			size = st.Size()
		}
		m.Databases = append(m.Databases, Database{Name: d.Name, File: rel, Format: "pg_dump custom (pg_restore)", SizeBytes: size})
	}
	return nil
}

func exportBuckets(opts Options, m *Manifest, logf func(string, ...any)) error {
	info, err := storage.BucketsInfo(opts.APIURL, opts.APIToken)
	if err != nil {
		if isNotConfigured(err) {
			m.Warnings = append(m.Warnings, "managed storage is not available in this environment")
			return nil
		}
		return fmt.Errorf("buckets: %w", err)
	}
	for _, b := range info.Buckets {
		if b.DeploymentAlias != opts.Alias {
			continue
		}
		out := Bucket{Name: b.Name, Path: "buckets/" + b.Name}
		root := filepath.Join(opts.OutDir, "buckets", b.Name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		startAfter := ""
		for {
			page, err := storage.ListObjects(opts.APIURL, opts.APIToken, b.Name, "", startAfter, 0)
			if err != nil {
				return fmt.Errorf("list %s: %w", b.Name, err)
			}
			for _, obj := range page.Objects {
				rel, ok := safeRelPath(obj.Key)
				if !ok {
					out.Skipped = append(out.Skipped, obj.Key)
					m.Warnings = append(m.Warnings, fmt.Sprintf("bucket %s: key %q cannot be written as a file path and was skipped", b.Name, obj.Key))
					continue
				}
				path := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
				if err != nil {
					return err
				}
				n, err := storage.DownloadObject(opts.APIURL, opts.APIToken, b.Name, obj.Key, f)
				if cerr := f.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					return fmt.Errorf("download %s/%s: %w", b.Name, obj.Key, err)
				}
				out.Objects++
				out.Bytes += n
			}
			if !page.Truncated {
				break
			}
			startAfter = page.NextStartAfter
		}
		logf("  %s: %d object(s), %s", b.Name, out.Objects, storage.FormatBytes(out.Bytes))
		m.Buckets = append(m.Buckets, out)
	}
	return nil
}

// safeRelPath turns an object key into a relative path that stays under the
// bucket directory. Keys are "/"-separated by convention; a segment that is
// empty, "." or ".." makes the key unrepresentable.
func safeRelPath(key string) (string, bool) {
	if key == "" || strings.HasPrefix(key, "/") {
		return "", false
	}
	segs := strings.Split(key, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "", false
		}
		if strings.ContainsRune(s, filepath.Separator) && filepath.Separator != '/' {
			return "", false
		}
	}
	return filepath.Join(segs...), true
}

// envFileSet is what compose needs to know about the env files: the
// deployment-wide file and one per service that has service-scoped entries.
type envFileSet struct {
	App      string            // env/app.env
	Services map[string]string // service → env/<service>.env
}

// exportEnv writes env/app.env with the deployment's resolved environment and
// env/<service>.env for each service that has entries of its own. Secret
// values are blanked unless IncludeSecrets; platform-generated values
// (DATABASE_URL_*, STORAGE_*_*, DIBBLA_*) are kept as comments because they
// point at Dibbla and are re-pointed by the compose file.
func exportEnv(opts Options, app *apps.Deployment, m *Manifest, logf func(string, ...any)) (envFileSet, error) {
	files := envFileSet{Services: map[string]string{}}
	m.Env.SecretsIncluded = opts.IncludeSecrets

	write := func(rel, service string, vars []apps.EnvVariable) error {
		sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
		var b strings.Builder
		b.WriteString("# Exported from Dibbla — app " + app.Alias)
		if service != "" {
			b.WriteString(", service " + service)
		}
		b.WriteString("\n")
		if !opts.IncludeSecrets {
			b.WriteString("# Secret values were not exported (run with --include-secrets to get them). Fill in the blanks.\n")
		}
		b.WriteString("# Platform-generated values (DATABASE_URL_*, STORAGE_*, DIBBLA_*) point at Dibbla and are\n# commented out; docker-compose.yml sets local replacements.\n\n")
		for _, v := range vars {
			m.Env.Variables = append(m.Env.Variables, EnvVariable{Name: v.Name, Source: v.Source, Service: service, File: rel})
			switch {
			case isPlatformVar(v.Name) || v.Source == "platform":
				// Platform values carry credentials (a DATABASE_URL_* has the
				// role's password, STORAGE_*_SECRET_ACCESS_KEY is a key), so
				// they follow the secrets rule — and stay commented out even
				// when exported, because they point at Dibbla.
				if opts.IncludeSecrets {
					b.WriteString("# " + envfile.FormatLine(v.Name, v.Value) + "\n")
				} else {
					b.WriteString("# " + v.Name + "=  (platform-generated; value not exported)\n")
				}
			case v.Source == "inline":
				b.WriteString(envfile.FormatLine(v.Name, v.Value) + "\n")
			case opts.IncludeSecrets:
				b.WriteString(envfile.FormatLine(v.Name, v.Value) + "\n")
			default:
				b.WriteString("# " + v.Source + " secret; value not exported\n" + v.Name + "=\n")
			}
		}
		return writeFile(filepath.Join(opts.OutDir, filepath.FromSlash(rel)), []byte(b.String()))
	}

	doc, _, err := apps.GetEnv(opts.APIURL, opts.APIToken, app.Alias, "")
	if err != nil {
		return files, fmt.Errorf("environment: %w", err)
	}
	files.App = "env/app.env"
	if err := write(files.App, "", doc.Variables); err != nil {
		return files, err
	}
	m.Env.Files = append(m.Env.Files, files.App)
	logf("  %s: %d variable(s)", files.App, len(doc.Variables))

	for _, svc := range app.Services {
		// Only services with entries of their own get a file — the
		// deployment-wide file already carries global + app scopes.
		list, err := secrets.ListSecrets(opts.APIURL, opts.APIToken, app.Alias, svc.Name)
		if err != nil {
			return files, fmt.Errorf("service %s secrets: %w", svc.Name, err)
		}
		if len(list.Secrets) == 0 {
			continue
		}
		sdoc, _, err := apps.GetEnv(opts.APIURL, opts.APIToken, app.Alias, svc.Name)
		if err != nil {
			return files, fmt.Errorf("service %s environment: %w", svc.Name, err)
		}
		var own []apps.EnvVariable
		for _, v := range sdoc.Variables {
			if v.Source == "service" {
				own = append(own, v)
			}
		}
		rel := "env/" + svc.Name + ".env"
		if err := write(rel, svc.Name, own); err != nil {
			return files, err
		}
		files.Services[svc.Name] = rel
		m.Env.Files = append(m.Env.Files, rel)
		logf("  %s: %d service variable(s)", rel, len(own))
	}
	return files, nil
}

func isPlatformVar(name string) bool {
	return strings.HasPrefix(name, "DATABASE_URL_") || strings.HasPrefix(name, "STORAGE_") || strings.HasPrefix(name, "DIBBLA_")
}

// exportManifestFile copies the app's dibbla.yaml out of the source when it
// has one, else writes a minimal one from the deployed configuration so the
// export always carries a manifest.
func exportManifestFile(opts Options, m *Manifest) error {
	m.ManifestFile = "dibbla.yaml"
	if m.Source != nil {
		for _, name := range []string{"dibbla.yaml", "dibbla.yml"} {
			data, err := os.ReadFile(filepath.Join(opts.OutDir, "source", name))
			if err == nil {
				m.ManifestOrigin = "source"
				return writeFile(filepath.Join(opts.OutDir, "dibbla.yaml"), data)
			}
		}
	}
	m.ManifestOrigin = "generated"
	return writeFile(filepath.Join(opts.OutDir, "dibbla.yaml"), []byte(generatedManifest(m)))
}

func generatedManifest(m *Manifest) string {
	var b strings.Builder
	b.WriteString("# Generated by dibbla export from the deployed configuration of " + m.App.Alias + ".\n")
	b.WriteString("# The app was deployed without a dibbla.yaml; this is the equivalent single-service manifest.\n")
	b.WriteString("version: 1\nservices:\n")
	if len(m.App.Services) == 0 {
		b.WriteString("  " + m.App.Alias + ":\n    build: .\n    public: true\n")
		if m.App.Port != nil {
			fmt.Fprintf(&b, "    port: %d\n", *m.App.Port)
		}
		if m.App.Replicas != nil {
			fmt.Fprintf(&b, "    replicas: %d\n", *m.App.Replicas)
		}
		if m.App.CPU != "" {
			fmt.Fprintf(&b, "    cpu: %q\n", m.App.CPU)
		}
		if m.App.Memory != "" {
			fmt.Fprintf(&b, "    memory: %q\n", m.App.Memory)
		}
		return b.String()
	}
	for _, s := range m.App.Services {
		b.WriteString("  " + s.Name + ":\n")
		if s.IsBuilt || s.Image == "" {
			b.WriteString("    build: .\n")
		} else {
			fmt.Fprintf(&b, "    image: %q\n", s.Image)
		}
		if s.Port != nil {
			fmt.Fprintf(&b, "    port: %d\n", *s.Port)
		}
		if s.IsPublic {
			b.WriteString("    public: true\n")
		}
		if s.Replicas > 0 {
			fmt.Fprintf(&b, "    replicas: %d\n", s.Replicas)
		}
	}
	return b.String()
}

func isNotConfigured(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NOT_CONFIGURED") || strings.Contains(s, "status 503")
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
