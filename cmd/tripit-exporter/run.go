package main

import (
	"fmt"
	"io"
)

// run parses the subcommand and returns the exit code. It never calls
// os.Exit, so a test can call it directly and check the exit code, the
// output, and the files it writes to OUTPUT_DIR.
func run(args []string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: a subcommand is required")
		return 2
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: unknown subcommand %q\n", args[0])
		return 2
	}
}
