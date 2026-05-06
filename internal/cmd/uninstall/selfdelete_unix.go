//go:build !windows

package uninstall

import (
	"fmt"
	"os"
)

// selfDeleteBinary removes the running binary. On Unix, unlinking your
// own executable is allowed: the kernel keeps the inode alive until
// every file descriptor (including the loaded image) is closed. The
// process keeps running normally and the file disappears from disk
// immediately.
func selfDeleteBinary(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
