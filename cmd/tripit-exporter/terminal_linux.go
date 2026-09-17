//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal, using the same ioctl that
// cookieInputMode uses.
func isTerminal(f *os.File) bool {
	var term syscall.Termios
	return ioctl(f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term))) == nil
}

// cookieInputMode prepares the terminal f for a pasted cookie, with the Linux
// ioctl from the syscall package. It turns off the echo, so the cookie never
// appears on screen. It also turns off canonical mode, because in that mode
// the terminal cuts a line at 4095 bytes, and a full Cookie header from a
// browser can be longer. The returned func restores the original terminal
// state; the caller must call it.
func cookieInputMode(f *os.File) (restore func(), err error) {
	fd := f.Fd()

	var term syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, uintptr(unsafe.Pointer(&term))); err != nil {
		return nil, err
	}
	original := term

	term.Lflag &^= syscall.ECHO | syscall.ICANON
	term.Cc[syscall.VMIN] = 1
	term.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(&term))); err != nil {
		return nil, err
	}

	return func() {
		_ = ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(&original)))
	}, nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); errno != 0 {
		return errno
	}
	return nil
}
