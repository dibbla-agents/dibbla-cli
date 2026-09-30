package create

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/preflight"
)

// The MCP server template (DIB-1225): a Go tool server whose dibbla.yaml
// publishes it as an MCP of its own, so `dibbla deploy` in the created
// project is the whole way to /platform/servers/<name>. It is a module of its
// own inside the Go worker starter template, in a directory the worker's
// build ignores and `create go-worker` removes.
//
// The placeholders below are the template's own values. They are part of its
// contract with this command: the template's CI keeps them in place.
const (
	mcpTemplateDir     = "_optional/mcp"
	mcpTemplateModule  = templateModule + "/" + mcpTemplateDir
	mcpTemplateName    = "my-mcp"
	mcpTemplateAddress = "grpc.dibbla.com:443"
)

// MCPTemplateRepoEnv points the command at another copy of the starter
// template: a fork, a branch checkout on disk, or a staging repository.
const MCPTemplateRepoEnv = "DIBBLA_MCP_TEMPLATE_REPO"

// mcpNameRe is narrower than what an MCP address allows (letters, digits,
// '.', '_' and '-'). The name is also the project directory, and `dibbla
// deploy` takes the app's alias from the directory, so it has to be a valid
// alias too. One name for all three is what makes create + deploy work with
// no flags.
var mcpNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}[a-z0-9]$`)

// MCPNameRule says what ValidateMCPName accepts, for prompts and errors.
const MCPNameRule = "3 to 64 characters: lowercase letters, digits and '-', starting with a letter and not ending with '-'"

// ValidateMCPName checks a name for `dibbla create mcp`.
func ValidateMCPName(name string) error {
	if !mcpNameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use %s. The name becomes the project directory, the app's alias and the last part of the MCP address", name, MCPNameRule)
	}
	return nil
}

// GrpcAddressFromAPIURL derives the address a tool server connects to from
// the Dibbla API URL: grpc.<domain>:443 sits next to api.<domain>, the same
// convention `dibbla mcp` uses for mcp.<domain>.
func GrpcAddressFromAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("no API URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	host := u.Hostname()
	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("host %q does not start with \"api.\"", host)
	}
	return "grpc." + strings.TrimPrefix(host, "api.") + ":443", nil
}

// MCPConfig is what `dibbla create mcp` decides before any file is written.
type MCPConfig struct {
	// Name is the project directory, the tool server name and the MCP
	// address's last segment.
	Name string
	// GrpcAddress is where the deployed tool server connects, host:port.
	GrpcAddress string
}

// MCP creates an MCP server project from the template in a new directory
// named cfg.Name. Nothing in it needs Go on this machine: the template
// carries its go.sum, and the platform builds the image.
func MCP(cfg MCPConfig) error {
	if err := ValidateMCPName(cfg.Name); err != nil {
		return err
	}
	if cfg.GrpcAddress == "" {
		return fmt.Errorf("no gRPC address for the tool server")
	}
	if err := preflight.RequireTool("git"); err != nil {
		return err
	}

	repo := templateRepo
	if v := strings.TrimSpace(os.Getenv(MCPTemplateRepoEnv)); v != "" {
		repo = v
	}
	// The whole starter is cloned next to the destination and only the MCP
	// directory is kept. The caller checked that cfg.Name did not exist, so
	// both directories are ours to remove on any failure.
	checkout := cfg.Name + ".template"
	if _, err := os.Stat(checkout); err == nil {
		return fmt.Errorf("%q is in the way; move it aside or create the project elsewhere", checkout)
	}
	defer os.RemoveAll(checkout)
	clone := exec.Command("git", "clone", "--quiet", "--depth", "1", repo, checkout)
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("could not fetch the template from %s: %w", repo, err)
	}
	if err := os.Rename(filepath.Join(checkout, filepath.FromSlash(mcpTemplateDir)), cfg.Name); err != nil {
		return fmt.Errorf("the template has no %s directory; this CLI and the template are out of step — update the CLI (`dibbla update`): %w", mcpTemplateDir, err)
	}
	if err := personalizeMCP(cfg); err != nil {
		_ = os.RemoveAll(cfg.Name)
		return err
	}
	return nil
}

// personalizeMCP turns the template directory into the user's project: the
// user's name where the template has its placeholder, and the gRPC address
// of the user's installation.
func personalizeMCP(cfg MCPConfig) error {
	manifest := filepath.Join(cfg.Name, "dibbla.yaml")
	before, err := os.ReadFile(manifest)
	if err != nil {
		return fmt.Errorf("the template has no dibbla.yaml: %w", err)
	}
	// Without the line that publishes the service the project would deploy
	// and publish nothing, silently. Refuse a template that lost it.
	if !strings.Contains(string(before), "mcp: "+mcpTemplateName) {
		return fmt.Errorf("the template's dibbla.yaml has no `mcp: %s` line; this CLI and the template are out of step — update the CLI (`dibbla update`)", mcpTemplateName)
	}

	return filepath.Walk(cfg.Name, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch {
		case info.Name() == "go.mod", strings.HasSuffix(info.Name(), ".go"):
			return replaceInFile(path, mcpTemplateModule, cfg.Name)
		case info.Name() == "dibbla.yaml", info.Name() == "env.example", info.Name() == "README.md":
			if err := replaceInFile(path, mcpTemplateName, cfg.Name); err != nil {
				return err
			}
			return replaceInFile(path, mcpTemplateAddress, cfg.GrpcAddress)
		}
		return nil
	})
}
