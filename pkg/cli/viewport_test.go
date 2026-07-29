package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestViewportPlainPathStreamsFinishedRows proves that off a terminal (a
// bytes.Buffer is never a TTY) the viewport degrades to plain append-only
// lines: nothing on start, one clean line per finished table, and never a
// cursor escape — so pipes, CI, and tests stay deterministic.
func TestViewportPlainPathStreamsFinishedRows(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	vp := newViewport(&buf, nil, 2)
	if vp.animate {
		t.Fatal("viewport must not animate when writing to a non-terminal buffer")
	}

	vp.start("core-capability/ka-capital", 1)
	if buf.Len() != 0 {
		t.Fatalf("start printed on the plain path: %q", buf.String())
	}
	vp.finish("core-capability/ka-capital", 1, 0)
	vp.start("safety-conduct/refusal-harmful", 1)
	vp.finish("safety-conduct/refusal-harmful", 0, 1)
	vp.close()

	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("plain path emitted an ANSI escape: %q", out)
	}
	for _, want := range []string{"core-capability/ka-capital", "1 passed", "safety-conduct/refusal-harmful", "1 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q; got:\n%s", want, out)
		}
	}
	if vp.finished != 2 || vp.passed != 1 || vp.failed != 1 {
		t.Errorf("totals = finished %d passed %d failed %d, want 2/1/1", vp.finished, vp.passed, vp.failed)
	}
}

// TestViewportPassOnlyRowIsGreenCheck proves a table with no failures renders a
// ✓ row, and one with failures renders a ✗ row with both tallies.
func TestViewportRowSymbols(t *testing.T) {
	t.Parallel()
	vp := newViewport(&bytes.Buffer{}, nil, 1)
	if got := vp.rowText(vpRow{label: "p/t", passed: 3}); !strings.Contains(got, "✓") || !strings.Contains(got, "3 passed") {
		t.Errorf("pass-only row = %q, want ✓ and '3 passed'", got)
	}
	got := vp.rowText(vpRow{label: "p/t", passed: 2, failed: 1})
	if !strings.Contains(got, "✗") || !strings.Contains(got, "2 passed") || !strings.Contains(got, "1 failed") {
		t.Errorf("failing row = %q, want ✗ and both tallies", got)
	}
}

// TestViewportAnimatedFrameRedraws forces the animate path (white-box) and
// checks a rendered frame carries the completed rows, the spinning running row,
// the footer, and an in-place cursor move — without asserting the exact cursor
// arithmetic.
func TestViewportAnimatedFrameRedraws(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	vp := newViewport(&buf, nil, 5)
	vp.animate = true // force the terminal path without a real TTY
	vp.color = false  // keep assertions about plain content simple
	vp.now = func() time.Time { return time.Date(2026, 1, 1, 0, 1, 5, 0, time.UTC) }
	vp.startedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	vp.done = []vpRow{{label: "a/done1", passed: 1}, {label: "a/done2", passed: 1, failed: 1}}
	vp.running = []vpRow{{label: "a/running", running: true}}
	vp.finished = 2
	vp.passed = 3
	vp.failed = 1
	vp.render()

	out := buf.String()
	for _, want := range []string{"a/done1", "a/done2", "a/running", "running…", "Tables 2/5", "1:05"} {
		if !strings.Contains(out, want) {
			t.Errorf("animated frame missing %q; got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\x1b[2K") {
		t.Error("animated frame should clear lines with \\x1b[2K")
	}
}

func TestPadOrTrim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"exact", 5, "exact"},
	}
	for _, tt := range tests {
		if got := padOrTrim(tt.in, tt.width); got != tt.want {
			t.Errorf("padOrTrim(%q,%d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}

// TestTruncateVisible is the guard for the narrow-terminal corruption bug: a
// line must be capped at `width` VISIBLE columns (ANSI escapes not counted, so
// they never push content past the terminal edge into a soft-wrap).
func TestTruncateVisible(t *testing.T) {
	t.Parallel()
	if got := truncateVisible("hello world", 5); got != "hello"+ansiReset {
		t.Errorf("plain truncate = %q, want %q", got, "hello"+ansiReset)
	}
	if got := truncateVisible("hi", 10); got != "hi" {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	if got := truncateVisible("abc", 0); got != "abc" {
		t.Errorf("width 0 should be unchanged, got %q", got)
	}
	// A colored line: the escape codes are copied but do not count toward width,
	// so exactly `width` visible runes survive.
	colored := ansiGreen + "✓" + ansiReset + " abcdefgh"
	got := truncateVisible(colored, 3) // "✓", " ", "a"
	if !strings.Contains(got, "✓") || !strings.Contains(got, "a") || strings.Contains(got, "b") {
		t.Errorf("colored truncate to 3 visible = %q, want '✓ a' worth of content", got)
	}
	// Visible length (escapes stripped) must not exceed the width.
	if vis := visibleLen(got); vis > 3 {
		t.Errorf("visible length %d exceeds width 3: %q", vis, got)
	}
}

// visibleLen counts runes outside ANSI escape sequences, for the test above.
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		_, size := decodeRune(s[i:])
		n++
		i += size
	}
	return n
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			// length of the first rune
			return r, len(string(r))
		}
	}
	return 0, 1
}

func TestElapsed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{65 * time.Second, "1:05"},
		{9 * time.Second, "0:09"},
		{-1, "0:00"},
	}
	for _, tt := range tests {
		if got := elapsed(tt.d); got != tt.want {
			t.Errorf("elapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
