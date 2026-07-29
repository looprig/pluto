//go:build darwin

package cli

// tiocgwinsz is the macOS/BSD TIOCGWINSZ ioctl request number.
const tiocgwinsz uintptr = 0x40087468
