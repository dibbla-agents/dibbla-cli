package deploy

import (
	"fmt"

	"github.com/dibbla-agents/dibbla-cli/internal/manifest"
)

// validateLocalManifest scans the project root for a dibbla.yaml/dibbla.yml.
// When present, parses + validates locally and returns a friendly error so
// the user fails fast before the archive upload. Absent → no-op.
func validateLocalManifest(projectRoot string) error {
	path, ambiguous, found := manifest.Discover(projectRoot)
	if !found {
		return nil
	}
	if ambiguous {
		return fmt.Errorf("both dibbla.yaml and dibbla.yml are present at %s; remove one", projectRoot)
	}
	if _, err := manifest.ParseAndValidate(path); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}
	return nil
}
