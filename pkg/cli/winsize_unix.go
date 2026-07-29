//go:build linux || darwin

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// winsize mirrors the kernel struct filled by the TIOCGWINSZ ioctl.
type winsize struct{ row, col, xpixel, ypixel uint16 }

// terminalSize returns f's terminal size in columns and rows, or (0, 0) when it
// cannot be determined (f is not a terminal, or the ioctl fails). The animated
// viewport uses the width to keep every line from soft-wrapping and the height
// to keep the live region from exceeding the screen — either of which would
// otherwise break the in-place cursor redraw.
func terminalSize(f *os.File) (cols, rows int) {
	var ws winsize
	// #nosec G103 -- the TIOCGWINSZ ioctl requires a pointer to a fixed local
	// struct; ws is not attacker-controlled and does not escape.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), tiocgwinsz, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0
	}
	return int(ws.col), int(ws.row)
}
