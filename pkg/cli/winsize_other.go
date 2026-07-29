//go:build !linux && !darwin

package cli

import "os"

// terminalSize is unavailable on this platform; returning zeroes disables the
// animated viewport so output degrades to safe plain append-only lines.
func terminalSize(_ *os.File) (cols, rows int) { return 0, 0 }
