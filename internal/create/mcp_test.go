package create

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/manifest"
)

// templateFixture is the part of the MCP template this command depends on:
// the placeholders it replaces. The template's own repository owns the real
// content; there it sits under _optional/mcp in the Go worker starter.
var templateFixture = map[string]string{
	"go.mod":  "module " + mcpTemplateModule + "\n\ngo 1.23.1\n",
	"main.go": "package main\n\nimport \"" + mcpTemplateModule + "/tools\"\n\nfunc main() { tools.Register() }\n",
	"dibbla.yaml": `version: 1

services:
  mcp:
    build: .
    port: 8080
    public: true
    mcp: my-mcp
    auth:
      require_login: true
      access_policy: all_members
    environment:
      SERVER_NAME: my-mcp
      GRPC_SERVER_ADDRESS: grpc.dibbla.com:443
      GRPC_USE_TLS: "true"
`,
	"env.example": "SERVER_NAME=my-mcp-local\nGRPC_SERVER_ADDRESS=grpc.dibbla.com:443\n",
	"README.md":   "dibbla mcp server my-mcp\n",
	"Dockerfile":  "FROM scratch\n",
}

// starterFixture is the repository `create mcp` clones: the Go worker
// starter, with the MCP template in its own directory.
func starterFixture() map[string]string {
	files := map[string]string{
		"go.mod":             "module " + templateModule + "\n\ngo 1.23.1\n",
		"cmd/worker/main.go": "package main\n\nfunc main() {}\n",
	}
	for name, content := range templateFixture {
		files[mcpTemplateDir+"/"+name] = content
	}
	return files
}

func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestValidateMCPName(t *testing.T) {
	for _, ok := range []string{"my-tools", "abc", "a1-b2", "a" + strings.Repeat("b", 63)} {
		if err := ValidateMCPName(ok); err != nil {
			t.Errorf("ValidateMCPName(%q) = %v, want nil", ok, err)
		}
	}
	// Each of these is either not an MCP address, not an app alias, or not a
	// directory name a deploy can take its alias from.
	for _, bad := range []string{"", "ab", "My-Tools", "my_tools", "my.tools", "1tools", "tools-", "my tools", "a/b", "a" + strings.Repeat("b", 64)} {
		if err := ValidateMCPName(bad); err == nil {
			t.Errorf("ValidateMCPName(%q) = nil, want an error", bad)
		}
	}
}

func TestGrpcAddressFromAPIURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.dibbla.com":       "grpc.dibbla.com:443",
		"https://api.example.org/":     "grpc.example.org:443",
		"api.example.org":              "grpc.example.org:443",
		"https://api.example.org:8443": "grpc.example.org:443",
	} {
		got, err := GrpcAddressFromAPIURL(in)
		if err != nil || got != want {
			t.Errorf("GrpcAddressFromAPIURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "https://dibbla.example.org", "http://localhost:8080"} {
		if got, err := GrpcAddressFromAPIURL(in); err == nil {
			t.Errorf("GrpcAddressFromAPIURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestPersonalizeMCP(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "acme-tools", templateFixture)

	if err := personalizeMCP(MCPConfig{Name: "acme-tools", GrpcAddress: "grpc.example.org:443"}); err != nil {
		t.Fatalf("personalizeMCP: %v", err)
	}

	yaml := read(t, "acme-tools/dibbla.yaml")
	for _, want := range []string{"mcp: acme-tools", "SERVER_NAME: acme-tools", "GRPC_SERVER_ADDRESS: grpc.example.org:443", "access_policy: all_members"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("dibbla.yaml lacks %q:\n%s", want, yaml)
		}
	}
	if strings.Contains(yaml, "my-mcp") || strings.Contains(yaml, "grpc.dibbla.com") {
		t.Errorf("dibbla.yaml still carries a placeholder:\n%s", yaml)
	}
	// What `dibbla deploy` will check before uploading.
	if _, err := manifest.ParseAndValidateBytes([]byte(yaml)); err != nil {
		t.Errorf("the created manifest does not validate: %v", err)
	}

	if got := read(t, "acme-tools/go.mod"); !strings.HasPrefix(got, "module acme-tools\n") {
		t.Errorf("go.mod = %q", got)
	}
	if got := read(t, "acme-tools/main.go"); !strings.Contains(got, `"acme-tools/tools"`) {
		t.Errorf("main.go import not rewritten: %q", got)
	}
	if got := read(t, "acme-tools/env.example"); !strings.Contains(got, "SERVER_NAME=acme-tools-local") || !strings.Contains(got, "grpc.example.org:443") {
		t.Errorf("env.example = %q", got)
	}
	if got := read(t, "acme-tools/README.md"); !strings.Contains(got, "dibbla mcp server acme-tools") {
		t.Errorf("README.md = %q", got)
	}
}

// A template without the publishing line would deploy fine and publish
// nothing. The command refuses it instead of handing over such a project.
func TestPersonalizeMCPRefusesATemplateWithoutThePublishingLine(t *testing.T) {
	t.Chdir(t.TempDir())
	files := map[string]string{}
	for k, v := range templateFixture {
		files[k] = v
	}
	files["dibbla.yaml"] = strings.Replace(files["dibbla.yaml"], "    mcp: my-mcp\n", "", 1)
	writeFixture(t, "acme-tools", files)

	err := personalizeMCP(MCPConfig{Name: "acme-tools", GrpcAddress: "grpc.example.org:443"})
	if err == nil || !strings.Contains(err.Error(), "mcp: my-mcp") {
		t.Fatalf("personalizeMCP = %v, want the out-of-step error", err)
	}
}

func TestMCPClonesTheTemplate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	writeFixture(t, repo, starterFixture())
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "template"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv(MCPTemplateRepoEnv, "file://"+filepath.ToSlash(repo))
	t.Chdir(t.TempDir())

	if err := MCP(MCPConfig{Name: "acme-tools", GrpcAddress: "grpc.example.org:443"}); err != nil {
		t.Fatalf("MCP: %v", err)
	}
	if got := read(t, "acme-tools/dibbla.yaml"); !strings.Contains(got, "mcp: acme-tools") {
		t.Errorf("dibbla.yaml = %q", got)
	}
	for _, stray := range []string{"acme-tools/.git", "acme-tools/cmd", "acme-tools.template"} {
		if _, err := os.Stat(stray); !os.IsNotExist(err) {
			t.Errorf("%s is left behind", stray)
		}
	}

	// A template that cannot be fetched leaves nothing behind.
	t.Setenv(MCPTemplateRepoEnv, "file://"+filepath.ToSlash(filepath.Join(repo, "missing")))
	if err := MCP(MCPConfig{Name: "other-tools", GrpcAddress: "grpc.example.org:443"}); err == nil {
		t.Fatal("MCP with an unreachable template: want an error")
	}
	if _, err := os.Stat("other-tools"); !os.IsNotExist(err) {
		t.Error("a failed create left a directory behind")
	}
}
