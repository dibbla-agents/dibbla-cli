package output

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// PrintYAML marshals data to YAML and writes it to stdout.
func PrintYAML(data interface{}) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	fmt.Print(string(out))
	return nil
}

// PrintJSON marshals data to indented JSON and writes it to stdout.
func PrintJSON(data interface{}) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// PrintTable prints tabular data with headers using tabwriter.
func PrintTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)

	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, h)
	}
	fmt.Fprintln(w)

	for _, row := range rows {
		for i, col := range row {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, col)
		}
		fmt.Fprintln(w)
	}

	w.Flush()
}

// Stderr prints a formatted message to stderr with a trailing newline.
func Stderr(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
