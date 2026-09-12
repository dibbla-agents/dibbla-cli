package render

// Exit codes for `dibbla deploy`. 2 is a build failure the customer can act
// on; ExitPlatformUnavailable says the platform's own registry or build
// service was down (REGISTRY_UNAVAILABLE / BUILD_SERVICE_UNAVAILABLE, both
// 503 from deploy-api, DIB-810). A CI gate must be able to tell "fix your
// Dockerfile" from "retry later" without parsing text, so the two never
// share a code — even though a registry outage also surfaces as a failed
// `export-image` step.
const (
	ExitBuildFailed         = 2
	ExitPlatformUnavailable = 20
)

// IsPlatformUnavailable reports whether the failure is the platform's own
// infrastructure rather than anything in the deployed source.
func IsPlatformUnavailable(e *DeployError) bool {
	if e == nil || e.APIError == nil {
		return false
	}
	switch e.APIError.Code {
	case "REGISTRY_UNAVAILABLE", "BUILD_SERVICE_UNAVAILABLE":
		return true
	}
	return false
}

// exitCodeFor is the one place the ladder is decided; every renderer and the
// structured stderr event go through it so exit_code never disagrees with
// the process exit status.
func exitCodeFor(e *DeployError) int {
	switch {
	case e == nil:
		return 0
	case IsPlatformUnavailable(e):
		return ExitPlatformUnavailable
	case e.FailedStep != "":
		return ExitBuildFailed
	}
	return 1
}
