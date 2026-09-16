//go:build !linux

package main

import "os"

// isTerminal always reports false outside Linux. The distroless image and
// the operator machine are both Linux; this build only keeps go vet and go
// test working on another OS during development.
func isTerminal(f *os.File) bool { return false }

func disableEcho(f *os.File) (restore func(), err error) {
	return func() {}, nil
}
