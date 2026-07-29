package cli

import (
	"fmt"
	"io"
	"os"
)

// ANSI SGR codes, emitted only when a ui has color enabled.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
	ansiGray   = "\x1b[90m"
)

// ui renders styled, optionally-colored command output. Color is on only when
// the destination is a real terminal and NO_COLOR is unset; a pipe, a
// redirected file, or a test buffer always gets plain text. verbose reveals
// detail() output — the "everything" that is hidden by default so a normal run
// shows only the useful summary.
type ui struct {
	w       io.Writer
	color   bool
	verbose bool
}

// newUI builds a ui for w. lookupEnv (App.LookupEnv) is consulted for the
// NO_COLOR convention; a nil lookupEnv just means "no NO_COLOR override".
func newUI(w io.Writer, lookupEnv func(string) (string, bool), verbose bool) *ui {
	color := isTerminalWriter(w)
	if lookupEnv != nil {
		if _, noColor := lookupEnv("NO_COLOR"); noColor {
			color = false
		}
	}
	return &ui{w: w, color: color, verbose: verbose}
}

// isTerminalWriter reports whether w is a character device (an interactive
// terminal). A non-*os.File writer — a pipe, a bytes.Buffer in tests — is
// never one, so those paths stay plain-text and deterministic.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in an SGR code when color is on, else returns it unchanged.
func (u *ui) paint(code, s string) string {
	if !u.color || code == "" {
		return s
	}
	return code + s + ansiReset
}

// title prints the command banner: a bold tag plus an optional dim subtitle.
func (u *ui) title(cmd, subtitle string) {
	tag := u.paint(ansiBold+ansiCyan, "mpqt "+cmd)
	if subtitle == "" {
		fmt.Fprintf(u.w, "\n%s\n", tag)
		return
	}
	fmt.Fprintf(u.w, "\n%s %s\n", tag, u.paint(ansiDim, subtitle))
}

// line writes a two-space-indented, symbol-prefixed message.
func (u *ui) line(symbol, code, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if symbol == "" {
		fmt.Fprintf(u.w, "  %s\n", msg)
		return
	}
	fmt.Fprintf(u.w, "  %s %s\n", u.paint(code, symbol), msg)
}

func (u *ui) ok(format string, a ...any)   { u.line("✓", ansiGreen, format, a...) }
func (u *ui) fail(format string, a ...any) { u.line("✗", ansiRed, format, a...) }
func (u *ui) warn(format string, a ...any) { u.line("!", ansiYellow, format, a...) }
func (u *ui) step(format string, a ...any) { u.line("›", ansiBlue, format, a...) }
func (u *ui) info(format string, a ...any) { u.line("·", ansiGray, format, a...) }

// detail prints dimmed secondary output, but ONLY in verbose mode. Route the
// noisy, exhaustive lines (per-table pricing caveats, missing-key notes, the
// full skipped list) through here so `--verbose` reveals them and a default
// run stays readable.
func (u *ui) detail(format string, a ...any) {
	if !u.verbose {
		return
	}
	fmt.Fprintf(u.w, "  %s\n", u.paint(ansiDim, fmt.Sprintf(format, a...)))
}

// verboseEnabled lets a caller skip building an expensive detail payload when
// detail output is suppressed.
func (u *ui) verboseEnabled() bool { return u.verbose }

// detailW returns a writer that reaches the terminal only in verbose mode, so
// a helper that writes its own plain notes (e.g. App.counterForPreflight) is
// silenced by default and surfaced under --verbose.
func (u *ui) detailW() io.Writer {
	if u.verbose {
		return u.w
	}
	return io.Discard
}

// blank prints an empty separating line.
func (u *ui) blank() { fmt.Fprintln(u.w) }

// badge returns a bold, colored status word (e.g. a disposition) padded with
// spaces so a colored terminal shows a filled chip; plain text elsewhere.
func (u *ui) badge(code, text string) string {
	if !u.color {
		return text
	}
	return u.paint(ansiBold+code, text)
}
