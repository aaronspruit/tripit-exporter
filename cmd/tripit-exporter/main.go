// Command tripit-exporter keeps a local archive of TripIt trips.
package main

import (
	"os"
	"strings"
	"time"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], environMap(os.Environ()), os.Stdin, os.Stdout, os.Stderr, time.Now()))
}

// environMap turns the "KEY=VALUE" slice from os.Environ into a map, so run
// can take its environment as an argument instead of reading it itself.
func environMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		key, value, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		env[key] = value
	}
	return env
}
