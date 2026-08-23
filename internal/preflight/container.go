package preflight

import (
	"fmt"
	"os"
	"os/exec"
)

// containerEnv is set by the published CI image (.github/docker/Dockerfile).
// Nothing else sets it, which makes it a more honest signal than probing for
// /.dockerenv — that is true of any container, including a full-fat one where
// git and npm are present and these commands work fine.
const containerEnv = "DIBBLA_IN_CONTAINER"

// InContainer reports whether this binary is running from the published Dibbla
// CI image.
func InContainer() bool {
	return os.Getenv(containerEnv) == "1"
}

// RequireTool checks that an external tool the CLI shells out to is on PATH,
// and returns an explanatory error if it is not.
//
// The CI image ships the static binary and nothing else, so git/go/npm are
// genuinely absent there by design. Without this the user gets Go's default
// "exec: \"git\": executable file not found in $PATH", which reads like a
// broken image rather than a documented boundary.
func RequireTool(tool string) error {
	if _, err := exec.LookPath(tool); err == nil {
		return nil
	}
	if InContainer() {
		return fmt.Errorf(
			"%[1]s is not available in the Dibbla CI image.\n"+
				"The image ships the CLI on its own, so the subcommands that shell out to %[1]s "+
				"(clone, create, and the preflight checks) do not run here.\n"+
				"The API-backed commands — deploy, apps, secrets, db — are what the image is for.",
			tool)
	}
	return fmt.Errorf("%s is required for this command but was not found on PATH", tool)
}
