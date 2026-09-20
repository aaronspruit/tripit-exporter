package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aaronspruit/tripit-exporter/internal/airtrail"
	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/secret"
)

// airtrailExitCode is the exit code of every AirTrail failure. It is its own
// code, and not 1, because 1 means that a TripIt credential needs replacing
// and an operator must be able to tell the two systems apart.
const airtrailExitCode = 3

// airtrailConfig is the AirTrail sync setting of a scheduled run.
type airtrailConfig struct {
	enabled bool
	baseURL string
	apiKey  string
	userID  string
	delete  bool
}

// readAirtrailConfig reads TRIPIT_AIRTRAIL_SYNC and the settings that go
// with it. When the sync is off, it reads nothing else. An error stops the
// run before any request.
func readAirtrailConfig(env map[string]string) (airtrailConfig, error) {
	cfg := airtrailConfig{enabled: boolEnv(env["TRIPIT_AIRTRAIL_SYNC"]), delete: true}
	if !cfg.enabled {
		return cfg, nil
	}
	cfg.baseURL = env["TRIPIT_AIRTRAIL_URL"]
	if cfg.baseURL == "" {
		return cfg, errors.New("TRIPIT_AIRTRAIL_URL is required when TRIPIT_AIRTRAIL_SYNC is true")
	}
	cfg.apiKey = secret.Read("airtrail_api_key", "TRIPIT_AIRTRAIL_API_KEY", env)
	if cfg.apiKey == "" {
		return cfg, errors.New("TRIPIT_AIRTRAIL_API_KEY is required when TRIPIT_AIRTRAIL_SYNC is true")
	}
	cfg.userID = env["TRIPIT_AIRTRAIL_USER_ID"]
	if v := env["TRIPIT_AIRTRAIL_DELETE"]; v != "" {
		cfg.delete = boolEnv(v)
	}
	return cfg, nil
}

// runAirtrail makes the flights of the AirTrail instance match the air
// segments of the archive. It runs after the merge and the refresh, so it
// reads the archive that this run just wrote.
func runAirtrail(env map[string]string, outputDir string, cfg airtrailConfig, stdout, stderr io.Writer) int {
	trips, err := archive.Load(outputDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return airtrailExitCode
	}

	wanted, warnings := airtrail.Build(trips, cfg.userID, airtrail.DefaultCodes())
	for _, w := range warnings {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: warning: airtrail:", w)
	}

	client := &airtrail.Client{
		BaseURL: cfg.baseURL,
		APIKey:  cfg.apiKey,
		HTTP:    http.DefaultClient,
		Verbose: boolEnv(env["TRIPIT_VERBOSE"]),
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(stdout, "tripit-exporter: airtrail: "+format+"\n", args...)
		},
	}

	_, _ = fmt.Fprintf(stdout, "tripit-exporter: airtrail: %d flights in the archive\n", len(wanted))
	result, syncWarnings, err := airtrail.Sync(context.Background(), client, outputDir, wanted, airtrail.Options{
		UserID: cfg.userID,
		Delete: cfg.delete,
	})
	for _, w := range syncWarnings {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: warning: airtrail:", w)
	}
	_, _ = fmt.Fprintf(stdout, "tripit-exporter: airtrail: %d added, %d updated, %d deleted, %d unchanged, %d failed\n",
		result.Added, result.Updated, result.Deleted, result.Unchanged, result.Failed)

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return airtrailExitCode
	}
	return 0
}
