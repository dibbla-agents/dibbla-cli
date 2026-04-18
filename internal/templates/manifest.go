// Package templates defines the dibbla-templates manifest schema and
// provides fetch + cache + embedded-fallback resolution for it.
package templates

import "os"

// DefaultManifestURL is the canonical location of the hosted manifest.
const DefaultManifestURL = "https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/templates.json"

// SupportedVersion is the manifest schema version this CLI understands.
const SupportedVersion = "1"

// Manifest is the top-level structure of templates.json.
type Manifest struct {
	Version   string     `json:"version"`
	Templates []Template `json:"templates"`
}

// Template describes a single template entry in the manifest.
type Template struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	BootstrapURL string `json:"bootstrap_url"`
	RepoURL      string `json:"repo_url"`
	TemplatePath string `json:"template_path"`
	IconURL      string `json:"icon_url,omitempty"`
	ReadmeURL    string `json:"readme_url,omitempty"`
}

// ManifestURL returns the effective manifest URL (env override wins).
func ManifestURL() string {
	if u := os.Getenv("DIBBLA_TEMPLATES_URL"); u != "" {
		return u
	}
	return DefaultManifestURL
}

// FindByID returns the template with the given ID, or nil.
func (m *Manifest) FindByID(id string) *Template {
	if m == nil {
		return nil
	}
	for i := range m.Templates {
		if m.Templates[i].ID == id {
			return &m.Templates[i]
		}
	}
	return nil
}
