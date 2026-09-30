package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/manifest"
)

// validateLocalManifest scans the project root for a dibbla.yaml/dibbla.yml.
// When present, applies shell-${VAR} substitution to the bytes (so error
// messages reference the resolved YAML the server will see), parses, and
// validates. Absent → no-op.
func validateLocalManifest(projectRoot string) error {
	_, err := localManifest(projectRoot)
	return err
}

// localManifest is validateLocalManifest with the parsed manifest handed
// back: nil when the project has none.
func localManifest(projectRoot string) (*manifest.Manifest, error) {
	path, ambiguous, found := manifest.Discover(projectRoot)
	if !found {
		return nil, nil
	}
	if ambiguous {
		return nil, fmt.Errorf("both dibbla.yaml and dibbla.yml are present at %s; remove one", projectRoot)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	subbed, err := SubstituteShellVarsFromOSEnv(raw)
	if err != nil {
		return nil, fmt.Errorf("manifest shell-var substitution: %w", err)
	}
	m, err := manifest.ParseAndValidateBytes(subbed)
	if err != nil {
		return nil, fmt.Errorf("manifest validation failed: %w", err)
	}
	return m, nil
}

// isRootManifestFile reports whether the given relative path (within the
// archive being created) is the dibbla manifest at the deploy root. Only
// root-level dibbla.yaml / dibbla.yml count — files of the same name inside
// subdirectories pass through unchanged, matching the manifest discovery
// rule (only the root is consulted).
func isRootManifestFile(relPath string) bool {
	clean := filepath.ToSlash(relPath)
	if strings.ContainsRune(clean, '/') {
		return false
	}
	return clean == "dibbla.yaml" || clean == "dibbla.yml"
}
