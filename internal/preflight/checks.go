package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CheckGo checks if Go is installed and prints the version
// Returns true if Go is available, false otherwise (but allows continue)
func CheckGo() bool {
	cmd := exec.Command("go", "version")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("  ⚠️  Go: not found (install from https://go.dev/dl/)")
		return false
	}

	// Parse version from "go version go1.23.4 windows/amd64"
	version := strings.TrimSpace(string(output))
	parts := strings.Fields(version)
	if len(parts) >= 3 {
		fmt.Printf("  ✅ Go: %s\n", parts[2])
	} else {
		fmt.Printf("  ✅ Go: installed\n")
	}
	return true
}

// CheckGit checks if Git is installed
// Returns true if Git is available, false otherwise
func CheckGit() bool {
	cmd := exec.Command("git", "--version")
	_, err := cmd.Output()
	return err == nil
}

// CheckNpm checks if npm is installed
// Returns true if npm is available, false otherwise
func CheckNpm() bool {
	cmd := exec.Command("npm", "--version")
	_, err := cmd.Output()
	return err == nil
}

// DirectoryExists checks if a directory already exists
func DirectoryExists(name string) bool {
	info, err := os.Stat(name)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

// ValidateToken checks if the token has the correct format
// Returns true if valid or empty, false if invalid format
func ValidateToken(token string) bool {
	if token == "" {
		return true // Empty is allowed (with warning)
	}
	return strings.HasPrefix(token, "ak_")
}

