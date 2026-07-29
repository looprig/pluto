package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// spinnerFrames is the braille spinner cycle animated on the running row.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	// viewportKeep is how many completed rows stay visible above the running
	// row (so the live region shows viewportKeep+1 table lines, pnpm-style).
	viewportKeep = 3
	// spinnerInterval paces the spinner animation.
	spinnerInterval = 90 * time.Millisecond
	// labelWidth pads the table label so rows align; long labels are truncated
	// to keep each line within a standard terminal width (no soft-wrap, which
	// would break the in-place cursor math).
	labelWidth = 42
	// minAnimateWidth is the narrowest terminal the animated viewport runs in.
	// Below it (or when the width is unknown), output degrades to plain
	// append-only lines, which never soft-wrap into corruption.
	minAnimateWidth = 40
)

// vpRow is one completed or running table line.
type vpRow struct {
	label   string
	passed  int
	failed  int
	running bool
}

// viewport is a pnpm-style live region: up to viewportKeep completed rows, one
// spinning running row, and a progress footer, redrawn in place on a terminal.
// Off a terminal (a pipe, a redirected file, CI, NO_COLOR, or a test buffer) it
// degrades to plain append-only lines — one per finished table — and never
// emits a cursor escape, so logs and test output stay clean and deterministic.
//
// It is safe for concurrent use: a background ticker animates the spinner while
// the caller blocks on a table, while start/finish/close mutate state from the
// caller's goroutine. mu guards every state access and every write to w.
type viewport struct {
	w         io.Writer
	color     bool
	animate   bool
	width     int // terminal width in columns; lines are truncated to it
	height    int // terminal height in rows; the live region is capped to it
	startedAt time.Time
	now       func() time.Time

	mu       sync.Mutex
	done     []vpRow // last completed rows, len <= viewportKeep
	running  []vpRow // tables currently executing (>1 under --concurrency)
	total    int
	finished int
	passed   int
	failed   int
	spin     int
	drawn    int // lines emitted in the last frame (for in-place redraw)
	closed   bool
	stop     chan struct{}
	tickDone chan struct{}
}

// newViewport builds a viewport writing to w. total is the number of runnable
// tables (for the footer's "d/total"). Animation and color are enabled only for
// a real terminal with NO_COLOR unset.
func newViewport(w io.Writer, lookupEnv func(string) (string, bool), total int) *viewport {
	term := isTerminalWriter(w)
	color := term
	if lookupEnv != nil {
		if _, noColor := lookupEnv("NO_COLOR"); noColor {
			color = false
		}
	}
	// Animate only when the terminal is wide enough that our fixed layout will
	// not soft-wrap; otherwise (narrow pane, or width unknown) fall back to
	// plain append-only lines that cannot corrupt the display.
	width, height := 0, 0
	if f, ok := w.(*os.File); ok && term {
		width, height = terminalSize(f)
	}
	animate := term && width >= minAnimateWidth
	vp := &viewport{
		w: w, color: color, animate: animate, width: width, height: height, total: total,
		now: time.Now, stop: make(chan struct{}), tickDone: make(chan struct{}),
	}
	vp.startedAt = time.Now()
	if vp.animate {
		fmt.Fprint(w, "\x1b[?25l") // hide cursor for the animation
		go vp.tickLoop()
	}
	return vp
}

// tickLoop advances and redraws the spinner while a table is running.
func (vp *viewport) tickLoop() {
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	defer close(vp.tickDone)
	for {
		select {
		case <-vp.stop:
			return
		case <-t.C:
			vp.mu.Lock()
			if len(vp.running) > 0 && !vp.closed {
				vp.spin = (vp.spin + 1) % len(spinnerFrames)
				vp.render()
			}
			vp.mu.Unlock()
		}
	}
}

// start marks a table (by label) as running. Multiple tables may run at once
// under --concurrency, so each gets its own row. On a terminal it draws the
// live region; off one it prints nothing (finish prints the line).
func (vp *viewport) start(label string, _ int) {
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if vp.closed {
		return
	}
	vp.running = append(vp.running, vpRow{label: label, running: true})
	if vp.animate {
		vp.render()
	}
}

// finish records a table's pass/fail outcome by label, removing it from the
// running set and scrolling it into the completed rows (terminal) or printing
// one plain line (off terminal), and updates the footer totals.
func (vp *viewport) finish(label string, passed, failed int) {
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if vp.closed {
		return
	}
	idx := -1
	for i := range vp.running {
		if vp.running[i].label == label {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}
	r := vp.running[idx]
	r.running, r.passed, r.failed = false, passed, failed
	vp.running = append(vp.running[:idx], vp.running[idx+1:]...)
	vp.finished++
	vp.passed += passed
	vp.failed += failed

	if !vp.animate {
		fmt.Fprintln(vp.w, "  "+vp.rowText(r))
		return
	}
	vp.done = append(vp.done, r)
	if len(vp.done) > viewportKeep {
		vp.done = vp.done[len(vp.done)-viewportKeep:]
	}
	vp.render()
}

// close stops the animation and erases the live region, leaving the cursor at
// the region's top so the caller can print the final report in its place. On
// the plain path it is a no-op beyond marking closed.
func (vp *viewport) close() {
	if vp.animate {
		close(vp.stop)
		<-vp.tickDone
	}
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if vp.closed {
		return
	}
	vp.closed = true
	if vp.animate {
		if vp.drawn > 0 {
			fmt.Fprintf(vp.w, "\x1b[%dA\x1b[0J", vp.drawn) // up over the region, clear to end of screen
			vp.drawn = 0
		}
		fmt.Fprint(vp.w, "\x1b[?25h") // restore cursor
	}
}

// render redraws the whole live region in place. Caller holds mu.
func (vp *viewport) render() {
	lines := vp.frameLines()
	var b strings.Builder
	if vp.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", vp.drawn) // cursor to top of previous frame
	}
	for _, ln := range lines {
		b.WriteString("\x1b[2K") // clear the line
		b.WriteString(truncateVisible(ln, vp.width))
		b.WriteByte('\n')
	}
	// Wipe any lines the previous, taller frame left behind, then move the
	// cursor back to the bottom of the new frame.
	if extra := vp.drawn - len(lines); extra > 0 {
		for i := 0; i < extra; i++ {
			b.WriteString("\x1b[2K\n")
		}
		fmt.Fprintf(&b, "\x1b[%dA", extra)
	}
	vp.drawn = len(lines)
	fmt.Fprint(vp.w, b.String())
}

// frameLines builds the region: completed rows, the running row, a rule, and
// the footer. Caller holds mu.
func (vp *viewport) frameLines() []string {
	rows := make([]vpRow, 0, len(vp.done)+len(vp.running))
	rows = append(rows, vp.done...)
	rows = append(rows, vp.running...)
	// Keep the region shorter than the terminal: rule + footer take two lines,
	// plus one row left for the shell prompt. When there are more rows than
	// fit, drop the oldest (completed) ones — the running rows at the tail stay
	// visible. A region taller than the screen would scroll and break the
	// in-place redraw just as soft-wrapping does.
	if vp.height > 4 {
		if maxRows := vp.height - 3; len(rows) > maxRows {
			rows = rows[len(rows)-maxRows:]
		}
	}
	lines := make([]string, 0, len(rows)+2)
	for _, r := range rows {
		lines = append(lines, "  "+vp.rowText(r))
	}
	lines = append(lines, "  "+vp.paint(ansiDim, strings.Repeat("─", vp.labelCols()+16)))
	lines = append(lines, "  "+vp.footerText())
	return lines
}

// labelCols is the label column width, shrunk on a narrow terminal so the
// pass/fail tally still fits on the line instead of being truncated away.
func (vp *viewport) labelCols() int {
	if vp.width <= 0 {
		return labelWidth
	}
	cols := vp.width - 26 // room for indent, symbol, spaces, and the tally
	if cols < 12 {
		cols = 12
	}
	if cols > labelWidth {
		cols = labelWidth
	}
	return cols
}

// rowText renders one row: a status symbol, the padded label, and either a
// pass/fail tally or the running marker.
func (vp *viewport) rowText(r vpRow) string {
	label := padOrTrim(r.label, vp.labelCols())
	if r.running {
		return fmt.Sprintf("%s %s %s",
			vp.paint(ansiYellow, spinnerFrames[vp.spin]), label, vp.paint(ansiYellow, "running…"))
	}
	sym, tally := vp.paint(ansiGreen, "✓"), vp.paint(ansiGreen, fmt.Sprintf("%d passed", r.passed))
	if r.failed > 0 {
		sym = vp.paint(ansiRed, "✗")
		tally = fmt.Sprintf("%s · %s", vp.paint(ansiGreen, fmt.Sprintf("%d passed", r.passed)),
			vp.paint(ansiRed, fmt.Sprintf("%d failed", r.failed)))
	}
	return fmt.Sprintf("%s %s %s", sym, label, tally)
}

// footerText renders the live totals line.
func (vp *viewport) footerText() string {
	scen := fmt.Sprintf("%s %s", vp.paint(ansiGreen, fmt.Sprintf("%d ✓", vp.passed)),
		vp.paint(ansiRed, fmt.Sprintf("%d ✗", vp.failed)))
	return fmt.Sprintf("%s %d/%d    %s %s    %s %s",
		vp.paint(ansiBold, "Tables"), vp.finished, vp.total,
		vp.paint(ansiBold, "Scenarios"), scen,
		vp.paint(ansiBold, "Elapsed"), elapsed(vp.now().Sub(vp.startedAt)))
}

func (vp *viewport) paint(code, s string) string {
	if !vp.color || code == "" {
		return s
	}
	return code + s + ansiReset
}

// truncateVisible caps a (possibly colored) line at width visible columns,
// copying ANSI escape sequences through without counting them and appending a
// reset if it cut mid-string. width <= 0 leaves the line unchanged. This is the
// safety net that guarantees no rendered line exceeds the terminal width and
// soft-wraps — the failure that corrupts the in-place redraw on a narrow pane.
func truncateVisible(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b { // ANSI escape ...m: copy verbatim, costs no columns
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++ // include the terminating 'm'
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if visible >= width {
			b.WriteString(ansiReset)
			return b.String()
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		visible++
		i += size
	}
	return b.String()
}

// padOrTrim pads s with spaces to width, or truncates it with an ellipsis so a
// long label never soft-wraps and breaks the redraw.
func padOrTrim(s string, width int) string {
	r := []rune(s)
	if len(r) > width {
		if width <= 1 {
			return string(r[:width])
		}
		return string(r[:width-1]) + "…"
	}
	return s + strings.Repeat(" ", width-len(r))
}

// elapsed formats a duration as m:ss.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}
