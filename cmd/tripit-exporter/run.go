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
	case "backfill":
		return runBackfill(env, stdin, stdout, stderr, now)
	default:
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: unknown subcommand %q\n", args[0])
		return 2
	}
}

// defaultOutputDir is the archive folder when OUTPUT_DIR is empty. The
// Compose file and the CronJob mount the archive volume there.
const defaultOutputDir = "/data"

// outputDir returns env["OUTPUT_DIR"], or defaultOutputDir when it is empty.
func outputDir(env map[string]string) string {
	if dir := env["OUTPUT_DIR"]; dir != "" {
		return dir
	}
	return defaultOutputDir
}

// runFeed fetches the TripIt calendar feed and merges it into the archive
// under outputDir(env). A successful merge is followed by the JSON refresh
// when TRIPIT_JSON_REFRESH is true, and then by the AirTrail sync when
// TRIPIT_AIRTRAIL_SYNC is true. See the README for the exit code table.
func runFeed(env map[string]string, stdout, stderr io.Writer, now time.Time) int {
	feedURL := secret.Read("tripit_feed_url", "TRIPIT_FEED_URL", env)
	if feedURL == "" {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: TRIPIT_FEED_URL is required")
		return 2
	}
	outputDir := outputDir(env)
	refresh, err := readRefreshConfig(env, outputDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}
	airtrailCfg, err := readAirtrailConfig(env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
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
	if refresh.enabled {
		if code := runRefresh(env, outputDir, refresh, stdout, stderr, now); code != 0 {
			return code
		}
	}
	if !airtrailCfg.enabled {
		return 0
	}
	return runAirtrail(env, outputDir, airtrailCfg, stdout, stderr)
}
