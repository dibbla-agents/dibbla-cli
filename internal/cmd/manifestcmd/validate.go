package manifestcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dibbla-agents/dibbla-cli/internal/manifest"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

var (
	validateTargetEnv string
	validateProfiles  []string
	validateNoPublic  bool
	validateJSON      bool
)

var validateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate a dibbla.yaml manifest locally",
	Long: `Validate a dibbla.yaml manifest locally without contacting the server.

Local validation covers the rules that don't need deploy-time context:
  - schema version
  - service name shape and reserved names
  - exactly one of build/image per service
  - image references must include a tag
  - port range

Env-aware resolution, quota checks, and multi-public detection happen on the
server. For those, use 'dibbla preview'.

Exits 0 if valid (or if no manifest is found and the dir would deploy via the
legacy single-Dockerfile path) and 1 with the error code on the first failure.

Examples:
  dibbla manifest validate              # validate ./dibbla.yaml
  dibbla manifest validate ./myapp      # validate ./myapp/dibbla.yaml
  dibbla manifest validate --json       # machine-readable output`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runValidate(os.Stdout, os.Stderr, args))
	},
}

func init() {
	validateCmd.Flags().StringVar(&validateTargetEnv, "target-env", "", "Manifest env name to record in the report (informational; resolution is server-side)")
	validateCmd.Flags().StringArrayVar(&validateProfiles, "profile", nil, "Activate a manifest profile (repeatable; informational)")
	validateCmd.Flags().BoolVar(&validateNoPublic, "no-public", false, "Allow zero public services (informational; the local check accepts both)")
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "Emit a structured JSON report instead of human text")
}

// validateError mirrors *manifest.Error for the JSON shape so consumers can
// share parsers with deploy-api's PreviewResponse.
type validateError struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

// serviceReport is one row of the human / JSON output describing a service.
type serviceReport struct {
	Name   string `json:"name"`
	Public bool   `json:"public"`
	Build  bool   `json:"build"`
	Image  string `json:"image,omitempty"`
}

// validateReport is the top-level JSON shape emitted with --json.
type validateReport struct {
	Valid        bool            `json:"valid"`
	ManifestPath string          `json:"manifest_path,omitempty"`
	NoManifest   bool            `json:"no_manifest,omitempty"`
	Services     []serviceReport `json:"services,omitempty"`
	Errors       []validateError `json:"errors,omitempty"`
	TargetEnv    string          `json:"target_env,omitempty"`
	Profiles     []string        `json:"profiles,omitempty"`
	NoPublic     bool            `json:"no_public,omitempty"`
}

// runValidate is the testable core. Returns the exit code (0 valid, 1 invalid).
// Side-effect-free apart from writing to stdout / stderr.
func runValidate(stdout, stderr io.Writer, args []string) int {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return emitFailure(stdout, stderr, "", []validateError{{
			Code:   "MANIFEST_INVALID",
			Detail: fmt.Sprintf("invalid path %q: %v", root, err),
		}})
	}

	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return emitFailure(stdout, stderr, "", []validateError{{
			Code:   "MANIFEST_INVALID",
			Detail: fmt.Sprintf("path not found: %s", abs),
		}})
	case err != nil:
		return emitFailure(stdout, stderr, "", []validateError{{
			Code:   "MANIFEST_INVALID",
			Detail: err.Error(),
		}})
	}

	// If the user pointed directly at a yaml file, validate that file.
	// Otherwise, treat as project root and discover.
	var path string
	switch {
	case !info.IsDir() && (filepath.Base(abs) == "dibbla.yaml" || filepath.Base(abs) == "dibbla.yml"):
		path = abs
	case !info.IsDir():
		return emitFailure(stdout, stderr, "", []validateError{{
			Code:   "MANIFEST_INVALID",
			Detail: fmt.Sprintf("file %s is not a dibbla manifest (expected dibbla.yaml or dibbla.yml)", abs),
		}})
	default:
		p, ambiguous, found := manifest.Discover(abs)
		switch {
		case ambiguous:
			return emitFailure(stdout, stderr, abs, []validateError{{
				Code:   manifest.ErrCodeManifestAmbiguous,
				Detail: fmt.Sprintf("both dibbla.yaml and dibbla.yml are present at %s; remove one", abs),
			}})
		case !found:
			return emitNoManifest(stdout, abs)
		default:
			path = p
		}
	}

	m, err := manifest.ParseAndValidate(path)
	if err != nil {
		return emitFailure(stdout, stderr, path, []validateError{toValidateError(err)})
	}

	return emitSuccess(stdout, path, m)
}

// emitNoManifest writes the informational "no manifest, legacy path" notice.
// Returns exit code 0 — absence is not a failure.
func emitNoManifest(stdout io.Writer, root string) int {
	if validateJSON {
		_ = json.NewEncoder(stdout).Encode(validateReport{
			Valid:      true,
			NoManifest: true,
			TargetEnv:  validateTargetEnv,
			Profiles:   validateProfiles,
			NoPublic:   validateNoPublic,
		})
		return 0
	}
	fmt.Fprintf(stdout, "%s no dibbla.yaml/dibbla.yml found in %s\n",
		platform.Icon("ℹ️", "[i]"), root)
	fmt.Fprintln(stdout, "  this directory will deploy via the legacy single-Dockerfile path")
	return 0
}

// emitSuccess prints the success report and returns exit code 0.
func emitSuccess(stdout io.Writer, path string, m *manifest.Manifest) int {
	services := summarizeServices(m)
	if validateJSON {
		_ = json.NewEncoder(stdout).Encode(validateReport{
			Valid:        true,
			ManifestPath: path,
			Services:     services,
			TargetEnv:    validateTargetEnv,
			Profiles:     validateProfiles,
			NoPublic:     validateNoPublic,
		})
		return 0
	}
	fmt.Fprintf(stdout, "%s %s is valid\n", platform.Icon("✓", "[OK]"), filepath.Base(path))
	if len(services) > 0 {
		labels := make([]string, 0, len(services))
		for _, s := range services {
			if s.Public {
				labels = append(labels, s.Name+" (public)")
			} else {
				labels = append(labels, s.Name)
			}
		}
		fmt.Fprintf(stdout, "  %d services: %s\n", len(services), join(labels, ", "))
	}
	if validateTargetEnv != "" {
		fmt.Fprintf(stdout, "  target env: %s (env-aware resolution runs server-side; use 'dibbla preview')\n", validateTargetEnv)
	}
	if len(validateProfiles) > 0 {
		fmt.Fprintf(stdout, "  profiles:   %s (profile filtering runs server-side; use 'dibbla preview')\n", join(validateProfiles, ", "))
	}
	return 0
}

// emitFailure prints the failure report and returns exit code 1.
func emitFailure(stdout, stderr io.Writer, path string, errs []validateError) int {
	if validateJSON {
		_ = json.NewEncoder(stdout).Encode(validateReport{
			Valid:        false,
			ManifestPath: path,
			Errors:       errs,
			TargetEnv:    validateTargetEnv,
			Profiles:     validateProfiles,
			NoPublic:     validateNoPublic,
		})
		return 1
	}
	label := "dibbla.yaml"
	if path != "" {
		label = filepath.Base(path)
	}
	fmt.Fprintf(stderr, "%s %s is invalid\n", platform.Icon("✗", "[X]"), label)
	for _, e := range errs {
		if e.Path != "" {
			fmt.Fprintf(stderr, "  %s: %s (%s)\n", e.Path, e.Detail, e.Code)
		} else {
			fmt.Fprintf(stderr, "  %s (%s)\n", e.Detail, e.Code)
		}
	}
	return 1
}

func toValidateError(err error) validateError {
	var me *manifest.Error
	if errors.As(err, &me) {
		return validateError{Code: me.Code, Path: me.Path, Detail: me.Detail}
	}
	return validateError{Code: "MANIFEST_INVALID", Detail: err.Error()}
}

// summarizeServices walks the manifest and produces a stable, sorted summary.
// `Public` is best-effort: true if the field is the bool literal `true`, OR if
// a per-env map carries `default: true`. Per-env-only public services may not
// be flagged as public locally; the server preview is authoritative.
func summarizeServices(m *manifest.Manifest) []serviceReport {
	out := make([]serviceReport, 0, len(m.Services))
	for name, svc := range m.Services {
		row := serviceReport{Name: name, Public: extractPublic(svc.Public), Build: svc.Build != nil}
		if ref, ok := svc.Image.(string); ok {
			row.Image = ref
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		// Public services first, then alphabetic for stability.
		if out[i].Public != out[j].Public {
			return out[i].Public
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// extractPublic best-effort coerces the manifest.Service.Public field to a bool.
// Accepts `bool` (scalar form) and `map[string]any` (env-aware form, checking
// "default" key). Returns false otherwise.
func extractPublic(v any) bool {
	switch p := v.(type) {
	case bool:
		return p
	case map[string]any:
		if def, ok := p["default"]; ok {
			if b, ok2 := def.(bool); ok2 {
				return b
			}
		}
	}
	return false
}

// join is a tiny helper to avoid a strings import for one call.
func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
