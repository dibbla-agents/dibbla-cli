package main

import (
	"os"

	"github.com/dibbla-agents/dibbla-cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

