//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal, using the same ioctl that
// disableEcho uses.
func isTerminal(f *os.File) bool {
	var term syscall.Termios
	return ioctl(f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term))) == nil
}

// disableEcho turns off terminal echo on f with the Linux ioctl from the
// syscall package, so a pasted cookie never appears on screen. The returned
// func restores the original terminal state; the caller must call it.
func disableEcho(f *os.File) (restore func(), err error) {
	fd := f.Fd()

	var term syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, uintptr(unsafe.Pointer(&term))); err != nil {
		return nil, err
	}
	original := term

	term.Lflag &^= syscall.ECHO
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
