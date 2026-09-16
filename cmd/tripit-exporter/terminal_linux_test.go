//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
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

func TestDisableEchoErrorsOnRegularFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "not-a-tty"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if _, err := disableEcho(f); err == nil {
		t.Fatal("disableEcho() = nil error for a regular file, want an error")
	}
}
