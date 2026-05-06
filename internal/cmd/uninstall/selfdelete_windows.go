//go:build windows

package uninstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// selfDeleteBinary cannot directly remove a running .exe on Windows.
// We write a short-lived helper batch file that waits for this process
// to exit, deletes the binary, then deletes itself, and detach-spawn
// it before returning. The user sees no console window.
func selfDeleteBinary(path string) error {
	tmp, err := os.CreateTemp("", "dibbla-uninstall-*.cmd")
	if err != nil {
		return fmt.Errorf("create helper script: %w", err)
	}
	tmpName := tmp.Name()

	script := `@echo off
:wait
del "` + path + `" >nul 2>&1
if exist "` + path + `" (
  timeout /t 1 /nobreak >nul
  goto wait
)
del "` + tmpName + `" >nul 2>&1
`
	if _, err := tmp.WriteString(script); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write helper script: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close helper script: %w", err)
	}

	cmd := exec.Command("cmd", "/c", filepath.Clean(tmpName))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("spawn helper: %w", err)
	}
	// Don't Wait — the helper outlives us by design.
	return nil
}
