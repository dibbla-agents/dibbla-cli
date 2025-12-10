package create

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	templateRepo   = "https://github.com/dibbla-agents/go-worker-starter-template.git"
	templateModule = "github.com/dibbla-agents/go-worker-starter-template"
)

// ProjectConfig holds the configuration for a new project
type ProjectConfig struct {
	Name            string
	Token           string
	IncludeFrontend bool
}

// GoWorker creates a new Go worker project from the template
func GoWorker(config ProjectConfig) error {
	// Step 1: Clone the template
	fmt.Println("  Cloning template...")
	if err := cloneTemplate(config.Name); err != nil {
		return fmt.Errorf("failed to clone template: %w", err)
	}

	// Step 2: Remove .git directory
	gitDir := filepath.Join(config.Name, ".git")
	if err := os.RemoveAll(gitDir); err != nil {
		return fmt.Errorf("failed to remove .git: %w", err)
	}

	// Step 3: Replace module path in all files
	fmt.Println("  Configuring module path...")
	if err := replaceModulePath(config.Name); err != nil {
		return fmt.Errorf("failed to replace module path: %w", err)
	}

	// Step 4: Create .env file
	fmt.Println("  Creating .env...")
	if err := createEnvFile(config.Name, config.Token); err != nil {
		return fmt.Errorf("failed to create .env: %w", err)
	}

	// Step 5: Handle frontend toggle
	if !config.IncludeFrontend {
		fmt.Println("  Removing frontend (not selected)...")
		if err := removeFrontend(config.Name); err != nil {
			return fmt.Errorf("failed to remove frontend: %w", err)
		}
	} else {
		// Install frontend dependencies
		fmt.Println("  Installing frontend dependencies...")
		if err := installFrontendDeps(config.Name); err != nil {
			// Non-fatal - warn but continue
			fmt.Printf("  ⚠️  Warning: npm install failed: %v\n", err)
			fmt.Println("     Run 'cd frontend && npm install' manually.")
		}
	}

	// Step 6: Clean up optional/docs folders
	fmt.Println("  Cleaning up...")
	if err := cleanupProject(config.Name); err != nil {
		// Non-fatal, just warn
		fmt.Printf("  ⚠️  Warning: cleanup had issues: %v\n", err)
	}

	// Step 7: Run go mod tidy
	fmt.Println("  Running go mod tidy...")
	if err := runGoModTidy(config.Name); err != nil {
		return fmt.Errorf("failed to run go mod tidy: %w", err)
	}

	return nil
}

func cloneTemplate(destDir string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", templateRepo, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func replaceModulePath(projectDir string) error {
	// Files to update: go.mod and all .go files
	return filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// Skip node_modules
		if info.IsDir() && info.Name() == "node_modules" {
			return filepath.SkipDir
		}

		// Only process go.mod and .go files
		if info.IsDir() {
			return nil
		}

		if info.Name() == "go.mod" || strings.HasSuffix(info.Name(), ".go") {
			return replaceInFile(path, templateModule, projectDir)
		}

		return nil
	})
}

func replaceInFile(filePath, oldStr, newStr string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	newContent := strings.ReplaceAll(string(content), oldStr, newStr)

	if string(content) != newContent {
		return os.WriteFile(filePath, []byte(newContent), 0644)
	}

	return nil
}

func createEnvFile(projectDir, token string) error {
	envPath := filepath.Join(projectDir, ".env")
	examplePath := filepath.Join(projectDir, "env.example")

	// Read env.example
	content, err := os.ReadFile(examplePath)
	if err != nil {
		// If env.example doesn't exist, create minimal .env
		content = []byte("SERVER_NAME=" + projectDir + "\nSERVER_API_TOKEN=your_api_token_here\n")
	}

	envContent := string(content)

	// Replace SERVER_NAME
	envContent = strings.Replace(envContent, "SERVER_NAME=my-worker", "SERVER_NAME="+projectDir, 1)

	// Replace SERVER_API_TOKEN if provided
	if token != "" {
		envContent = strings.Replace(envContent, "SERVER_API_TOKEN=your_api_token_here", "SERVER_API_TOKEN="+token, 1)
	}

	return os.WriteFile(envPath, []byte(envContent), 0644)
}

func removeFrontend(projectDir string) error {
	// Directories to remove when frontend is disabled
	dirsToRemove := []string{
		filepath.Join(projectDir, "frontend"),
		filepath.Join(projectDir, "internal", "frontend"),
		filepath.Join(projectDir, "internal", "http_handlers"),
	}

	for _, dir := range dirsToRemove {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	// Update main.go to remove frontend imports and code
	mainGoPath := filepath.Join(projectDir, "cmd", "worker", "main.go")
	return updateMainGoWithoutFrontend(mainGoPath)
}

func updateMainGoWithoutFrontend(mainGoPath string) error {
	content, err := os.ReadFile(mainGoPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	skipBlock := false
	skipHTTPConfig := false

	for _, line := range lines {
		// Skip net/http import (not needed without frontend)
		if strings.Contains(line, `"net/http"`) {
			continue
		}

		// Skip frontend-related imports
		if strings.Contains(line, `internal/frontend"`) ||
			strings.Contains(line, `internal/http_handlers/`) {
			continue
		}

		// Skip comment about frontend
		if strings.Contains(line, "Frontend and HTTP handlers (optional") {
			continue
		}

		// Skip HTTP server config block (starts with comment)
		if strings.Contains(line, "HTTP server config") {
			skipHTTPConfig = true
			continue
		}

		// End of HTTP config block (empty line or next comment)
		if skipHTTPConfig {
			if strings.TrimSpace(line) == "" || strings.Contains(line, "// Create SDK server") {
				skipHTTPConfig = false
				if strings.Contains(line, "// Create SDK server") {
					newLines = append(newLines, line)
				}
				continue
			}
			continue
		}

		// Skip HTTP server setup block
		if strings.Contains(line, "Start HTTP server with frontend") {
			skipBlock = true
			continue
		}

		// End of HTTP server block
		if skipBlock && strings.Contains(line, "}()") {
			skipBlock = false
			continue
		}

		if skipBlock {
			continue
		}

		// Skip router and http handler lines
		if strings.Contains(line, "router := frontend.NewRouter()") ||
			strings.Contains(line, "httpgreeting.Register(") ||
			strings.Contains(line, `HTTP: POST /api/greeting`) {
			continue
		}

		newLines = append(newLines, line)
	}

	// Clean up multiple consecutive empty lines
	cleanedContent := cleanEmptyLines(strings.Join(newLines, "\n"))

	return os.WriteFile(mainGoPath, []byte(cleanedContent), 0644)
}

func cleanEmptyLines(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	prevEmpty := false

	for _, line := range lines {
		isEmpty := strings.TrimSpace(line) == ""
		if isEmpty && prevEmpty {
			continue
		}
		result = append(result, line)
		prevEmpty = isEmpty
	}

	return strings.Join(result, "\n")
}

func cleanupProject(projectDir string) error {
	// Remove _optional directory (not needed in generated project)
	optionalDir := filepath.Join(projectDir, "_optional")
	if err := os.RemoveAll(optionalDir); err != nil {
		return err
	}

	// Remove nested template directory if it exists
	nestedTemplate := filepath.Join(projectDir, "go-worker-starter-template")
	if err := os.RemoveAll(nestedTemplate); err != nil {
		return err
	}

	return nil
}

func runGoModTidy(projectDir string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func installFrontendDeps(projectDir string) error {
	frontendDir := filepath.Join(projectDir, "frontend")
	
	// Check if npm is available
	checkCmd := exec.Command("npm", "--version")
	if err := checkCmd.Run(); err != nil {
		return fmt.Errorf("npm not found - install Node.js from https://nodejs.org")
	}
	
	cmd := exec.Command("npm", "install")
	cmd.Dir = frontendDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

