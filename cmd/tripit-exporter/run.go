package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/feed"
	"github.com/aaronspruit/tripit-exporter/internal/secret"
)

// run parses the subcommand and returns the exit code. It never calls
// os.Exit, so a test can call it directly and check the exit code, the
// output, and the files it writes to OUTPUT_DIR. With no subcommand, it
// runs the scheduled feed fetch. now is the fetch time, so a test never
// reads the real clock.
func run(args []string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return runFeed(env, stdout, stderr, now)
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

// runFeed fetches the TripIt calendar feed and merges it into the archive
// under env["OUTPUT_DIR"]. See docs/plan.md for the exit code table.
func runFeed(env map[string]string, stdout, stderr io.Writer, now time.Time) int {
	feedURL := secret.Read("tripit_feed_url", "TRIPIT_FEED_URL", env)
	if feedURL == "" {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: TRIPIT_FEED_URL is required")
		return 2
	}
	outputDir := env["OUTPUT_DIR"]
	if outputDir == "" {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: OUTPUT_DIR is required")
		return 2
	}

	calendar, rateLimited, err := feed.Fetch(context.Background(), http.DefaultClient, feedURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return feed.ExitCode(err)
	}
	if rateLimited {
		_, _ = fmt.Fprintln(stdout, "tripit-exporter: the feed rate-limited this run, no change")
		return 0
	}

	warnings, err := archive.Run(outputDir, calendar, now)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: warning:", w)
	}
	return 0
}
