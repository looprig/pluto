//go:build linux

package cli

// tiocgwinsz is the Linux TIOCGWINSZ ioctl request number.
const tiocgwinsz uintptr = 0x5413
