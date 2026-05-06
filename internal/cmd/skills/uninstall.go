package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UninstallSkill removes a previously installed skill at <root>:
//
//   - Deletes <root>/.claude/skills/<id>/ (and prunes empty parents up
//     through .claude/ if no other skills remain).
//   - Strips the marker-delimited dibbla pointer block from AGENTS.md
//     and GEMINI.md at <root>, leaving the rest of those files intact.
//     If a file becomes empty (or whitespace-only) after stripping, it
//     is removed.
//
// Returns the list of paths actually removed/modified, in the order
// they were touched. A best-effort attempt is made: errors on
// individual paths are reported but do not abort the whole operation.
func UninstallSkill(root, id string) (removed []string, errs []error) {
	if err := validateSkillID(id); err != nil {
		return nil, []error{err}
	}

	skillDir := filepath.Join(root, ".claude", "skills", id)
	if info, err := os.Stat(skillDir); err == nil && info.IsDir() {
		if err := os.RemoveAll(skillDir); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", skillDir, err))
		} else {
			removed = append(removed, skillDir)
			pruneEmptyDirs(filepath.Join(root, ".claude"), filepath.Dir(skillDir))
		}
	} else if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("stat %s: %w", skillDir, err))
	}

	for _, name := range []string{"AGENTS.md", "GEMINI.md"} {
		path := filepath.Join(root, name)
		changed, err := stripBlockFromFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if changed {
			removed = append(removed, path)
		}
	}

	return removed, errs
}

// stripBlockFromFile removes the marker block from the file at path.
// Returns (changed, err): changed is true if the file was modified or
// removed. A missing file is not an error.
func stripBlockFromFile(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	stripped, didStrip := stripMarkerBlock(string(raw))
	if !didStrip {
		return false, nil
	}

	// If the remaining file is whitespace-only, delete it.
	if strings.TrimSpace(stripped) == "" {
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("removing empty %s: %w", path, err)
		}
		return true, nil
	}
	if err := atomicWrite(path, []byte(stripped), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// stripMarkerBlock removes the first marker-delimited block from s.
// Returns (newContent, removed). If markers aren't both present (open
// before close), s is returned unchanged with removed=false.
//
// The block plus any immediately surrounding blank lines are removed,
// preserving the input's line-ending style.
func stripMarkerBlock(s string) (string, bool) {
	openIdx := strings.Index(s, markerOpen)
	if openIdx < 0 {
		return s, false
	}
	rel := strings.Index(s[openIdx:], markerClose)
	if rel < 0 {
		return s, false
	}
	closeEnd := openIdx + rel + len(markerClose)

	eol := "\n"
	if strings.Contains(s, "\r\n") {
		eol = "\r\n"
	}

	before := s[:openIdx]
	after := s[closeEnd:]

	// Trim a trailing blank-line gap left by the block.
	before = strings.TrimRight(before, "\r\n")
	after = strings.TrimLeft(after, "\r\n")

	switch {
	case before == "" && after == "":
		return "", true
	case before == "":
		return after + eol, true
	case after == "":
		return before + eol, true
	default:
		return before + eol + eol + after + eol, true
	}
}

// pruneEmptyDirs walks up from leaf to (but not past) ceiling, removing
// each directory that is empty after a child removal. Failures are
// silent: an unprunable parent (e.g. another skill installed) just
// stops the walk.
func pruneEmptyDirs(ceiling, leaf string) {
	cur := leaf
	for {
		if cur == "" || cur == ceiling || !strings.HasPrefix(cur, ceiling) {
			break
		}
		entries, err := os.ReadDir(cur)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(cur); err != nil {
			break
		}
		cur = filepath.Dir(cur)
	}
	// Try the ceiling itself last.
	if entries, err := os.ReadDir(ceiling); err == nil && len(entries) == 0 {
		_ = os.Remove(ceiling)
	}
}
