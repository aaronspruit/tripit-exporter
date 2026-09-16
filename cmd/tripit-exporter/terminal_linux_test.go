//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestIsTerminalFalseForRegularFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "not-a-tty"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if isTerminal(f) {
		t.Fatal("isTerminal() = true for a regular file")
	}
}

func TestCookieInputModeErrorsOnRegularFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "not-a-tty"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if _, err := cookieInputMode(f); err == nil {
		t.Fatal("cookieInputMode() = nil error for a regular file, want an error")
	}
}

// openPTY opens a pseudo-terminal pair: the test writes to main as a person
// types, and the code under test reads from tty.
func openPTY(t *testing.T) (main, tty *os.File) {
	t.Helper()
	main, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = main.Close() })

	var unlock int32
	if err := ioctl(main.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		t.Fatalf("unlock pty: %v", err)
	}
	var n uint32
	if err := ioctl(main.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		t.Fatalf("pty number: %v", err)
	}
	tty, err = os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	return main, tty
}

func TestReadCookieFromTerminalLongerThanTheLineLimit(t *testing.T) {
	main, tty := openPTY(t)
	cookie := "_abck=" + strings.Repeat("a", 6000)

	type result struct {
		cookie string
		err    error
	}
	done := make(chan result, 1)
	var stdout bytes.Buffer
	go func() {
		c, err := readCookie(tty, &stdout)
		done <- result{c, err}
	}()

	// Wait until readCookie has set the terminal mode, then paste the
	// cookie and press Enter.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var term syscall.Termios
		if err := ioctl(tty.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term))); err != nil {
			t.Fatal(err)
		}
		if term.Lflag&syscall.ECHO == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readCookie did not turn off the echo")
		}
		time.Sleep(10 * time.Millisecond)
	}
	go func() { _, _ = io.Copy(main, strings.NewReader(cookie+"\r")) }()
	go func() { _, _ = io.Copy(io.Discard, main) }()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("readCookie: %v", r.err)
		}
		if r.cookie != cookie {
			t.Fatalf("readCookie() has %d bytes, want %d", len(r.cookie), len(cookie))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readCookie did not return")
	}

	var term syscall.Termios
	if err := ioctl(tty.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term))); err != nil {
		t.Fatal(err)
	}
	if term.Lflag&(syscall.ECHO|syscall.ICANON) != syscall.ECHO|syscall.ICANON {
		t.Fatal("readCookie did not restore the echo and canonical mode")
	}
}
