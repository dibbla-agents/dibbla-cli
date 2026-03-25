package spinner

import (
	"fmt"
	"sync"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

// Start begins a spinner animation with the given message and optional ANSI
// color code (e.g. "\033[32m" for green, "" for no color).
// In CI environments it prints the message once with no animation.
// Returns a stop function that must be called to end the spinner.
func Start(message string, color string) func() {
	if platform.IsCI() {
		fmt.Printf("%s...\n", message)
		return func() {}
	}

	done := make(chan struct{})
	var once sync.Once

	go func() {
		if platform.SupportsUnicode() {
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					if color != "" {
						fmt.Printf("\r%s%s\033[0m %s...", color, frames[i%len(frames)], message)
					} else {
						fmt.Printf("\r%s %s...", frames[i%len(frames)], message)
					}
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		} else {
			frames := []string{"|", "/", "-", "\\"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r[%s] %s...", frames[i%len(frames)], message)
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
	}()

	return func() {
		once.Do(func() { close(done) })
	}
}
