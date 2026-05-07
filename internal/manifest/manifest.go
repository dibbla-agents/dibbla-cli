// Package manifest is the CLI-side dibbla.yaml validator. It mirrors the
// server's schema rules just enough to fail fast on common mistakes locally
// (mistyped service names, both yaml/yml present, missing build+image, etc.)
// before uploading the archive.
//
// The server in deploy-api/internal/manifest is the source of truth. CLI
// validation is best-effort — it does NOT do env-aware resolution or quota
// checks; those run server-side where the deploy context is fully known.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error codes — kept in sync with deploy-api/internal/manifest/errors.go so
// users see the same identifiers locally and from the server.
const (
	ErrCodeManifestAmbiguous     = "MANIFEST_AMBIGUOUS"
	ErrCodeManifestInvalid       = "MANIFEST_INVALID"
	ErrCodeManifestUnsupported   = "MANIFEST_UNSUPPORTED"
	ErrCodeServiceNameInvalid    = "SERVICE_NAME_INVALID"
	ErrCodeBuildContextMissing   = "BUILD_CONTEXT_MISSING"
	ErrCodeDockerfileMissing     = "DOCKERFILE_MISSING"
	ErrCodeStatefulNoVolume      = "STATEFUL_NO_VOLUME"
	ErrCodeRouteInvalid          = "ROUTE_INVALID"
)

// Error is the structured error returned by the CLI validator.
type Error struct {
	Code   string
	Path   string
	Detail string
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// Manifest is the parsed dibbla.yaml shape used for client-side validation.
// Fields are intentionally loose — env-aware fields are decoded as
// `interface{}` and inspected only enough to reject obvious mistakes.
type Manifest struct {
	Version  int                 `yaml:"version"`
	Services map[string]*Service `yaml:"services"`
}

// Service holds the per-service fields the CLI cares about for local
// validation. The server's manifest module has the complete shape.
type Service struct {
	Build       any         `yaml:"build,omitempty"`        // string or object
	Image       any         `yaml:"image,omitempty"`        // string or env-aware map
	Port        *int        `yaml:"port,omitempty"`
	Public      any         `yaml:"public,omitempty"`
	Replicas    any         `yaml:"replicas,omitempty"`
	CPU         any         `yaml:"cpu,omitempty"`
	Memory      any         `yaml:"memory,omitempty"`
	Environment any         `yaml:"environment,omitempty"`
	Command     []string    `yaml:"command,omitempty"`
	Entrypoint  []string    `yaml:"entrypoint,omitempty"`
	Volumes     []Volume    `yaml:"volumes,omitempty"`
	Profiles    []string    `yaml:"profiles,omitempty"`
	DependsOn   []string    `yaml:"depends_on,omitempty"`
	ExposeTo    []string    `yaml:"expose_to,omitempty"`
	Stateful    *bool       `yaml:"stateful,omitempty"`
	Routes      []Route     `yaml:"routes,omitempty"`
}

// Volume is a per-service persistent volume entry.
type Volume struct {
	Path   string `yaml:"path"`
	Size   string `yaml:"size"`
	Access string `yaml:"access,omitempty"`
}

// Route is one externally-routable endpoint on a service. Validation rules
// mirror deploy-api/internal/manifest.Route so users get matching error
// codes locally and from the server.
type Route struct {
	Type     string `yaml:"type"`               // "tcp" | "https" | "http"
	Port     int    `yaml:"port"`               // container port to forward to
	TLS      string `yaml:"tls,omitempty"`      // "edge" | "passthrough" | "none"
	Hostname string `yaml:"hostname,omitempty"` // subdomain label
}

// Discover scans the project root for dibbla.yaml/dibbla.yml and returns
// (path, ambiguous, found). When both are present, returns ("", true, true);
// the caller should error with ErrCodeManifestAmbiguous. When neither is
// present, returns ("", false, false).
func Discover(projectRoot string) (path string, ambiguous bool, found bool) {
	yamlPath := filepath.Join(projectRoot, "dibbla.yaml")
	ymlPath := filepath.Join(projectRoot, "dibbla.yml")
	yamlFound := fileExists(yamlPath)
	ymlFound := fileExists(ymlPath)
	if yamlFound && ymlFound {
		return "", true, true
	}
	if yamlFound {
		return yamlPath, false, true
	}
	if ymlFound {
		return ymlPath, false, true
	}
	return "", false, false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

var (
	serviceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)
	imageWithTagRe = regexp.MustCompile(`^[^\s]+:[^\s/:@]+$`)
	dnsLabelRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

var (
	validRouteTypes = map[string]bool{"tcp": true, "https": true, "http": true}
	validRouteTLS   = map[string]bool{"edge": true, "passthrough": true, "none": true}
)

var reservedServiceNames = map[string]bool{
	"proxy":       true,
	"auth":        true,
	"system":      true,
	"dibbla":      true,
	"kube-system": true,
}

// ParseAndValidate reads, parses, and validates the manifest at path.
// Returns the parsed Manifest on success, or *Error on the first failure.
//
// The validator covers schema rules that don't need deploy-time context
// (version, service names, build/image XOR, image-must-have-tag). Env-aware
// resolution and quota checks happen server-side.
func ParseAndValidate(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{Code: ErrCodeManifestInvalid, Detail: err.Error()}
	}
	return ParseAndValidateBytes(data)
}

// ParseAndValidateBytes is the byte-input variant of ParseAndValidate.
// Use this when the manifest content has already been read (e.g. after
// shell-var substitution by the deploy package) so the validator works
// against the resolved YAML, not the placeholder version.
func ParseAndValidateBytes(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, &Error{Code: ErrCodeManifestInvalid, Detail: "yaml parse: " + err.Error()}
	}
	if m.Version != 1 {
		return nil, &Error{Code: ErrCodeManifestInvalid, Path: "version",
			Detail: fmt.Sprintf("unsupported manifest version %d (must be 1)", m.Version)}
	}
	if len(m.Services) == 0 {
		return nil, &Error{Code: ErrCodeManifestInvalid, Path: "services",
			Detail: "at least one service is required"}
	}
	for name, svc := range m.Services {
		if err := validateServiceName(name); err != nil {
			return nil, err
		}
		if err := validateService(name, svc); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func validateServiceName(name string) error {
	if !serviceNameRe.MatchString(name) {
		return &Error{Code: ErrCodeServiceNameInvalid, Path: "services." + name,
			Detail: fmt.Sprintf("service name %q does not match %s", name, serviceNameRe.String())}
	}
	if reservedServiceNames[name] || strings.HasPrefix(name, "kube-") {
		return &Error{Code: ErrCodeServiceNameInvalid, Path: "services." + name,
			Detail: fmt.Sprintf("service name %q is reserved", name)}
	}
	return nil
}

func validateService(name string, s *Service) error {
	hasBuild := s.Build != nil
	hasImage := s.Image != nil
	switch {
	case hasBuild && hasImage:
		return &Error{Code: ErrCodeManifestInvalid, Path: "services." + name,
			Detail: "service must specify exactly one of `build` or `image`, not both"}
	case !hasBuild && !hasImage:
		return &Error{Code: ErrCodeManifestInvalid, Path: "services." + name,
			Detail: "service must specify either `build` or `image`"}
	}
	if s.Port != nil && (*s.Port < 1 || *s.Port > 65535) {
		return &Error{Code: ErrCodeManifestInvalid, Path: "services." + name + ".port",
			Detail: fmt.Sprintf("port %d out of range 1-65535", *s.Port)}
	}
	if hasImage {
		if ref, ok := s.Image.(string); ok && !imageWithTagRe.MatchString(ref) {
			return &Error{Code: ErrCodeManifestInvalid, Path: "services." + name + ".image",
				Detail: fmt.Sprintf("image reference %q must include a tag", ref)}
		}
	}
	if s.Stateful != nil && *s.Stateful && len(s.Volumes) == 0 {
		return &Error{Code: ErrCodeStatefulNoVolume, Path: "services." + name + ".stateful",
			Detail: "stateful: true requires at least one entry in volumes"}
	}
	for i, r := range s.Routes {
		if err := validateRoute(name, i, r); err != nil {
			return err
		}
	}
	return nil
}

func validateRoute(svc string, idx int, r Route) error {
	path := fmt.Sprintf("services.%s.routes[%d]", svc, idx)
	if r.Type == "" {
		return &Error{Code: ErrCodeRouteInvalid, Path: path + ".type",
			Detail: "route.type is required (one of tcp | https | http)"}
	}
	if !validRouteTypes[r.Type] {
		return &Error{Code: ErrCodeRouteInvalid, Path: path + ".type",
			Detail: fmt.Sprintf("route.type %q must be one of tcp | https | http", r.Type)}
	}
	if r.Port < 1 || r.Port > 65535 {
		return &Error{Code: ErrCodeRouteInvalid, Path: path + ".port",
			Detail: fmt.Sprintf("route.port must be between 1 and 65535, got %d", r.Port)}
	}
	if r.TLS != "" && !validRouteTLS[r.TLS] {
		return &Error{Code: ErrCodeRouteInvalid, Path: path + ".tls",
			Detail: fmt.Sprintf("route.tls %q must be one of edge | passthrough | none", r.TLS)}
	}
	if r.Type == "http" && r.TLS == "edge" {
		return &Error{Code: ErrCodeRouteInvalid, Path: path,
			Detail: "route.type=http with tls=edge is invalid (use type=https for managed TLS)"}
	}
	if r.Type == "tcp" && r.TLS == "none" {
		return &Error{Code: ErrCodeRouteInvalid, Path: path,
			Detail: "route.type=tcp with tls=none is not supported (no way to disambiguate without TLS SNI)"}
	}
	if r.Hostname != "" && !dnsLabelRe.MatchString(r.Hostname) {
		return &Error{Code: ErrCodeRouteInvalid, Path: path + ".hostname",
			Detail: fmt.Sprintf("route.hostname %q must be a valid DNS label (a-z, 0-9, -; max 63 chars)", r.Hostname)}
	}
	return nil
}
