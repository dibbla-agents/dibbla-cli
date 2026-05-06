package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// TTY renders the live buildkit-style deploy view from the design — a
// header, an in-place table of build steps, a sub-cell unicode progress
// bar, and on completion either the DEPLOYED summary block or a fenced
// BUILD OUTPUT block with parsed compile diagnostics. The renderer
// redraws the moving region on each event by emitting cursor-up + clear
// sequences so we never scroll the terminal during the build.
type TTY struct {
	w        io.Writer
	enableANSI bool // set false in tests for stable golden output

	startedAt   time.Time
	steps       []*ttyStep
	stepByIndex map[int]*ttyStep
	stepCount   int
	currentLog  string

	// state captured for the final summary block
	result *DeployResult
	errEv  *DeployError

	// number of lines drawn in the moving region last frame; we move the
	// cursor up by this many before redrawing to overwrite in place.
	lastDrawnLines int

	// drewHeader is set true once the static header (deploy name + revision
	// info) has been emitted; the header is drawn once and never redrawn.
	drewHeader bool
}

type ttyStep struct {
	idx     int
	step    string // short slug
	name    string // raw vertex name
	state   string // running|done|cached|fail|pending|log
	cached  bool
	elapsed int64
	logTail string
}

// NewTTY returns a renderer that targets an interactive terminal. Pass
// enableANSI=false from tests to get a deterministic, plain-text frame
// that's easy to assert against.
func NewTTY(w io.Writer, enableANSI bool) *TTY {
	return &TTY{
		w:           w,
		enableANSI:  enableANSI,
		startedAt:   time.Now(),
		stepByIndex: map[int]*ttyStep{},
	}
}

func (t *TTY) OnEvent(ev DeployEvent) {
	switch ev.Type {
	case "deploy":
		t.drawHeader(ev)
	case "build":
		t.handleBuild(ev)
		t.redraw()
	case "rollout":
		t.handleRollout(ev)
		t.redraw()
	case "result":
		t.result = ev.Result
		t.finalize()
	case "error":
		t.errEv = ev.Error
		t.finalize()
	}
}

func (t *TTY) OnDone() int {
	if t.errEv != nil {
		// 2 mirrors the design's `exit 2` for build failures; everything
		// else gets the generic exit 1.
		if t.errEv.FailedStep != "" {
			return 2
		}
		return 1
	}
	return 0
}

func (t *TTY) drawHeader(ev DeployEvent) {
	if t.drewHeader {
		return
	}
	t.drewHeader = true
	fmt.Fprintln(t.w)
	// "▸ dibbla deploy" — brand chevron + brand-bright bold name (the
	// app-identity cue the v2 design pushes).
	fmt.Fprintf(t.w, "%s  %s\n",
		t.paint("  ▸", colorBrand+colorBold),
		t.paint("dibbla deploy", colorBright+colorBold),
	)
	if ev.Source != "" {
		fmt.Fprintf(t.w, "    %s\n", t.paint(ev.Source, colorDim))
	}
	fmt.Fprintln(t.w)
	fmt.Fprintln(t.w, t.paint("BUILD", colorBrand+colorBold))
	fmt.Fprintln(t.w)
}

func (t *TTY) handleBuild(ev DeployEvent) {
	if ev.StepCount > t.stepCount {
		t.stepCount = ev.StepCount
	}
	if ev.StepIndex == 0 && ev.Step == "" {
		return
	}
	s, ok := t.stepByIndex[ev.StepIndex]
	if !ok {
		s = &ttyStep{idx: ev.StepIndex, step: ev.Step, name: ev.Name}
		t.stepByIndex[ev.StepIndex] = s
		t.steps = append(t.steps, s)
	}
	switch ev.State {
	case "running":
		s.state = "running"
		s.cached = ev.Cached
	case "done":
		s.state = "done"
		s.elapsed = ev.ElapsedMs
	case "cached":
		s.state = "cached"
		s.cached = true
		s.elapsed = ev.ElapsedMs
	case "fail":
		s.state = "fail"
		s.elapsed = ev.ElapsedMs
	case "log":
		s.logTail = trimRight(ev.Log)
		t.currentLog = s.logTail
	}
}

func (t *TTY) handleRollout(ev DeployEvent) {
	// Treat rollout phases as additional pseudo-steps so they share the
	// same rhythm as build steps in the table.
	idx := -1
	for _, s := range t.steps {
		if s.step == "rollout" {
			idx = s.idx
			break
		}
	}
	if idx < 0 {
		idx = len(t.steps) + 1
		t.steps = append(t.steps, &ttyStep{idx: idx, step: "rollout", name: "rollout"})
		t.stepByIndex[idx] = t.steps[len(t.steps)-1]
	}
	s := t.stepByIndex[idx]
	switch ev.State {
	case "rollout-start":
		s.state = "running"
		if ev.Source != "" {
			s.name = "rollout · " + ev.Source
		}
	case "rollout-done":
		s.state = "done"
	case "route-done":
		s.state = "done"
	}
}

// redraw clears the previously drawn moving region and re-emits the
// step table + progress bar. Steps drawn in the last frame are
// overwritten in place using cursor-up + erase-line sequences. When ANSI
// is disabled (test mode), each frame is appended without erasing — the
// final output is still asserted against an expected last-frame substring.
func (t *TTY) redraw() {
	if t.enableANSI && t.lastDrawnLines > 0 {
		fmt.Fprintf(t.w, "\033[%dF", t.lastDrawnLines) // move up N lines
		fmt.Fprint(t.w, "\033[J")                       // clear to end of screen
	}
	lines := t.frame()
	for _, line := range lines {
		fmt.Fprintln(t.w, line)
	}
	t.lastDrawnLines = len(lines)
}

func (t *TTY) frame() []string {
	out := make([]string, 0, len(t.steps)+4)
	total := max(t.stepCount, len(t.steps))
	for _, s := range t.steps {
		sigil := stateSigil(s.state, t.enableANSI)
		idxRaw := padRight(fmt.Sprintf("[%d/%d]", s.idx, total), 8)
		nameRaw := padRight(s.name, 30)
		elapsedRaw := padLeft(formatElapsed(s.elapsed), 6)

		// Color each column by the step's state — matches the v2 design's
		// per-state palette: indices in magenta (build identity), names in
		// bright when active / white when done, elapsed in brand when done
		// and bright while running.
		var idxColor, nameColor, elapsedColor string
		switch s.state {
		case "running":
			idxColor, nameColor, elapsedColor = colorMagenta, colorBright+colorBold, colorBright
		case "done":
			idxColor, nameColor, elapsedColor = colorMagenta, colorWhite, colorBrand
		case "cached":
			idxColor, nameColor, elapsedColor = colorMagenta, colorDim, colorDim
		case "fail":
			idxColor, nameColor, elapsedColor = colorRed, colorRed+colorBold, colorRed
		default: // pending
			idxColor, nameColor, elapsedColor = colorFaint, colorFaint, colorDim
		}
		line := fmt.Sprintf("%s %s  %s  %s",
			t.paint(idxRaw, idxColor),
			sigil,
			t.paint(nameRaw, nameColor),
			t.paint(elapsedRaw, elapsedColor),
		)
		out = append(out, line)
	}
	out = append(out, "")
	out = append(out, t.progressBar())
	if t.currentLog != "" {
		out = append(out, t.paint("  log  ", colorFaint)+t.paint(t.currentLog, colorBright))
	}
	return out
}

func (t *TTY) progressBar() string {
	total := max(t.stepCount, len(t.steps))
	if total == 0 {
		return ""
	}
	done := 0
	for _, s := range t.steps {
		if s.state == "done" || s.state == "cached" {
			done++
		}
	}
	pct := float64(done) / float64(total)
	const width = 42
	filled := int(pct * float64(width))
	partials := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
	partial := ""
	if frac := int((pct*float64(width) - float64(filled)) * 8); frac > 0 && frac < len(partials) {
		partial = partials[frac]
	}
	empty := width - filled
	if partial != "" {
		empty--
	}
	if empty < 0 {
		empty = 0
	}

	// Two-tone fill: trail (older progress) in brand sage, the leading
	// 2 cells + sub-cell partial in brand-bright. Reads as a glowing
	// edge advancing across the bar, matching the v2 design.
	lead := min(2, filled)
	trail := filled - lead
	bar := t.paint(strings.Repeat("█", trail), colorBrand) +
		t.paint(strings.Repeat("█", lead)+partial, colorBright) +
		t.paint(strings.Repeat("░", empty), colorFaint)
	pctText := fmt.Sprintf("%3.0f%%", pct*100)
	pctColor := colorBright
	if pct >= 1 {
		pctColor = colorBrand + colorBold
	}
	return fmt.Sprintf("  %s  %s%s%s%s",
		bar,
		t.paint(pctText, pctColor),
		t.paint("  ·  ", colorFaint),
		t.paint(fmt.Sprintf("step %d of %d", done, total), colorDim),
		"",
	)
}

// finalize prints the post-build summary block (DEPLOYED on success or
// BUILD OUTPUT block on failure). Called from OnEvent for the terminal
// `result` / `error` event so the frame is final before the program
// returns.
func (t *TTY) finalize() {
	if t.errEv != nil {
		t.printFailure()
		return
	}
	if t.result == nil {
		return
	}
	// BuildKit doesn't always emit a terminal "completed" event for its
	// transient internal vertices (load build definition, load
	// .dockerignore, load build context). Those steps stick at "running"
	// and the progress bar tops out at 80–95% even on a clean deploy.
	// Once the server confirms success, promote any leftover
	// running/pending steps to done and redraw one final frame so the
	// table reads 100% before the DEPLOYED block.
	for _, s := range t.steps {
		if s.state == "running" || s.state == "" {
			s.state = "done"
		}
	}
	t.redraw()

	fmt.Fprintln(t.w)
	fmt.Fprintln(t.w, t.paint("DEPLOYED", colorBrand+colorBold))
	t.prop("url", t.paint(t.result.Deployment.URL, colorCyan))
	t.prop("alias", t.paint(t.result.Deployment.Alias, colorBright+colorBold))
	t.prop("status", t.paint(t.result.Deployment.Status, colorBrand))
	if t.result.VCSCommit != "" {
		t.prop("revision", t.paint(t.result.VCSCommit, colorMagenta+colorBold))
	}
	t.printServicesTable()
	fmt.Fprintln(t.w)
	fmt.Fprintf(t.w, "  %s %s\n",
		t.paint("follow logs ·", colorDim),
		t.paint(fmt.Sprintf("dibbla logs %s -f", t.result.Deployment.Alias), colorBright),
	)
}

// printServicesTable prints a per-service summary line under the DEPLOYED
// block, but only when the deployment actually has a multi-service shape
// (more than one service, or a single service that isn't the synthesized
// "app"). Empty/legacy deployments produce no output (byte-stable with
// today's renderer).
func (t *TTY) printServicesTable() {
	svcs := t.result.Deployment.Services
	if len(svcs) == 0 {
		return
	}
	if len(svcs) == 1 && svcs[0].Name == "app" {
		return
	}
	fmt.Fprintln(t.w)
	fmt.Fprintln(t.w, t.paint("SERVICES", colorBrand+colorBold))
	for _, s := range svcs {
		role := "internal"
		if s.IsPublic {
			role = "public"
		}
		ready := fmt.Sprintf("%d/%d", s.ReadyReplicas, s.Replicas)
		status := s.Status
		if status == "" {
			status = "starting"
		}
		fmt.Fprintf(t.w, "  %s  %s  %s  %s\n",
			t.paint(padRight(s.Name, 12), colorBright+colorBold),
			t.paint(padRight(role, 8), colorDim),
			t.paint(padRight(status, 10), colorBrand),
			t.paint(ready, colorDim),
		)
	}
}

// prop renders one "label  value" line with the design's column rhythm:
// dim label padded to a fixed width, then the colored value.
func (t *TTY) prop(label, value string) {
	fmt.Fprintf(t.w, "  %s  %s\n", t.paint(padRight(label, 9), colorDim), value)
}

func (t *TTY) printFailure() {
	fmt.Fprintln(t.w)

	// Only render the fenced BUILD OUTPUT block when the failure has
	// real build context. Pre-build failures (auth, validation, archive
	// limits) just print the API error one-line — wrapping those in a
	// "step 0/0 (deploy)" frame is misleading.
	hasBuildContext := t.errEv.FailedStep != "" ||
		len(t.errEv.ParsedItems) > 0 ||
		t.errEv.BuildLogs != "" ||
		(t.errEv.APIError != nil && t.errEv.APIError.Logs != "")

	if hasBuildContext {
		failedStep := t.errEv.FailedStep
		if failedStep == "" {
			failedStep = "build"
		}
		// Opening fence stays red (it's the severity cue); closing fence
		// is dim per the v2 redesign — too many red lines on one screen
		// reads as "all-orange" alarm fatigue.
		header := fmt.Sprintf("──── BUILD OUTPUT · step %d/%d (%s) ────", t.errEv.StepIndex, t.errEv.StepCount, failedStep)
		fmt.Fprintln(t.w, t.paint(header, colorRed))

		// File paths in cyan (matches the source-column convention),
		// line/col in dim, message in white. Fall back to raw build logs
		// only when nothing could be parsed.
		switch {
		case len(t.errEv.ParsedItems) > 0:
			for _, p := range t.errEv.ParsedItems {
				fmt.Fprintf(t.w, "  %s%s %s\n",
					t.paint(p.File, colorCyan),
					t.paint(fmt.Sprintf(":%d:%d:", p.Line, p.Col), colorDim),
					t.paint(p.Message, colorWhite),
				)
			}
		case t.errEv.BuildLogs != "":
			fmt.Fprint(t.w, t.paint(tailLines(t.errEv.BuildLogs, 20), colorDim))
		case t.errEv.APIError != nil && t.errEv.APIError.Logs != "":
			fmt.Fprint(t.w, t.paint(tailLines(t.errEv.APIError.Logs, 20), colorDim))
		}
		fmt.Fprintln(t.w, t.paint("──── END BUILD OUTPUT ────", colorDim))
		fmt.Fprintln(t.w)
	}

	if t.errEv.APIError != nil {
		// "✗ CODE: message" — only the glyph + code carry red weight;
		// message in white so it's still legible without saturating.
		fmt.Fprintf(t.w, "  %s %s%s %s\n",
			t.paint("✗", colorRed+colorBold),
			t.paint(t.errEv.APIError.Code, colorRed+colorBold),
			t.paint(":", colorDim),
			t.paint(t.errEv.APIError.Message, colorWhite),
		)
	}
	if t.errEv.RetryCmd != "" {
		fmt.Fprintln(t.w)
		fmt.Fprintf(t.w, "  %s %s\n",
			t.paint("re-run ·", colorDim),
			t.paint(t.errEv.RetryCmd, colorBright),
		)
	}
}

// ── small helpers ──────────────────────────────────────────────────────

// Dibbla brand palette — truecolor codes mirror tokens.css from the
// design (--t-brand sage, --t-brand-bright, --t-magenta, --t-cyan,
// --t-red brick). Truecolor is widely supported in 2026 (iTerm2,
// kitty, Alacritty, Terminal.app, Windows Terminal); terminals that
// don't support it strip the sequences cleanly.
const (
	colorBrand   = "\033[38;2;118;179;96m"  // #76b360 sage — done / brand
	colorBright  = "\033[38;2;138;196;113m" // #8ac471 brand-bright — active
	colorMagenta = "\033[38;2;178;141;216m" // #b28dd8 — build identity (rev, step indices)
	colorCyan    = "\033[38;2;124;196;192m" // #7cc4c0 — sources, URLs, registries
	colorRed     = "\033[38;2;212;84;74m"   // #d4544a — true brick red, no orange cast
	colorWhite   = "\033[38;2;245;250;247m" // #f5faf7
	colorDim     = "\033[38;2;122;141;128m" // #7a8d80
	colorFaint   = "\033[38;2;74;91;81m"    // #4a5b51
	colorBold    = "\033[1m"
	colorReset   = "\033[0m"
)

// paint wraps text with an ANSI color sequence; no-op when ANSI is off
// (test mode) so golden output stays readable.
func (t *TTY) paint(text, color string) string {
	if !t.enableANSI || color == "" {
		return text
	}
	return color + text + colorReset
}

func stateSigil(state string, ansi bool) string {
	switch state {
	// Cached steps are still "good" so they sit in the brand family;
	// running steps lean on brand-bright (the active accent).
	case "done":
		if ansi {
			return colorBrand + colorBold + "✓" + colorReset
		}
		return "✓"
	case "cached":
		if ansi {
			return colorBrand + "⊙" + colorReset
		}
		return "⊙"
	case "running":
		if ansi {
			return colorBright + "⠿" + colorReset
		}
		return "⠿"
	case "fail":
		if ansi {
			return colorRed + colorBold + "✗" + colorReset
		}
		return "✗"
	default:
		return "○"
	}
}

func padRight(s string, n int) string {
	if len([]rune(s)) >= n {
		runes := []rune(s)
		return string(runes[:n])
	}
	return s + strings.Repeat(" ", n-len([]rune(s)))
}

func padLeft(s string, n int) string {
	if len([]rune(s)) >= n {
		return s
	}
	return strings.Repeat(" ", n-len([]rune(s))) + s
}

func trimRight(s string) string { return strings.TrimRight(s, "\r\n\t ") }

// tailLines returns at most n trailing lines of s with a leading
// "(elided …)" marker when content was dropped.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	dropped := len(lines) - n
	tail := strings.Join(lines[len(lines)-n:], "\n")
	return fmt.Sprintf("(elided %d earlier line(s) — pass --verbose-build to see all)\n%s\n", dropped, tail)
}
