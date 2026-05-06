package applogs

import (
	"encoding/json"
	"strings"
)

// FormatEntry renders one entry as `HH:MM:SS.mmm  LEVEL  body`. Level is
// extracted from JSON-shaped lines (slog format) when present; otherwise the
// timestamp + raw line are printed without a tag.
//
// When the line is plain text but a `level` label is present (the workflow
// run-logs endpoint serves entries this way — `[INFO] message` plus
// labels.level), the bracketed prefix is treated as the level.
func FormatEntry(e Entry, useColor bool) string {
	ts := e.Timestamp.Local().Format("15:04:05.000")
	level, msg := splitLevelAndMessage(e.Line)

	// Fallback: if the line itself didn't carry a level, look at labels.
	if level == "" && e.Labels != nil {
		if l, ok := e.Labels["level"]; ok && l != "" {
			level = l
			msg = e.Line
		}
	}

	if level == "" {
		return ts + "  " + e.Line
	}

	tag := padRight(strings.ToUpper(level), 5)
	if useColor {
		tag = colorize(level, tag)
	}
	return ts + "  " + tag + "  " + msg
}

func splitLevelAndMessage(line string) (level, msg string) {
	trimmed := strings.TrimSpace(line)

	// slog JSON shape: {"time":"...","level":"INFO","msg":"..."}
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var obj struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && obj.Msg != "" {
			return obj.Level, obj.Msg
		}
	}

	// Bracket-prefix shape: "[INFO] message text"
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "]"); end > 1 {
			candidate := trimmed[1:end]
			if isLevelWord(candidate) {
				rest := strings.TrimSpace(trimmed[end+1:])
				if rest != "" {
					return candidate, rest
				}
			}
		}
	}

	return "", line
}

func isLevelWord(s string) bool {
	switch strings.ToUpper(s) {
	case "DEBUG", "INFO", "WARN", "WARNING", "ERROR", "ERR", "FATAL", "PANIC":
		return true
	}
	return false
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func colorize(level, s string) string {
	switch strings.ToUpper(level) {
	case "ERROR", "ERR", "FATAL", "PANIC":
		return "\033[31m" + s + "\033[0m" // red
	case "WARN", "WARNING":
		return "\033[33m" + s + "\033[0m" // yellow
	case "INFO":
		return "\033[36m" + s + "\033[0m" // cyan
	case "DEBUG":
		return "\033[90m" + s + "\033[0m" // bright-black
	}
	return s
}
