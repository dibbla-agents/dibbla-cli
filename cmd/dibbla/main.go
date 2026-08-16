package main

import (
	"errors"
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/cmd"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}
	// Commands may carry a specific exit status so a caller can tell a
	// validation failure from a missing resource from a network problem
	// without scraping stderr. Anything else keeps the generic 1.
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		os.Exit(coded.ExitCode())
	}
	os.Exit(1)
}
