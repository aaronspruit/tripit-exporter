package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/secret"
	"github.com/aaronspruit/tripit-exporter/internal/tripitweb"
)

// sessionFile is the file in the output folder that holds the newest
// it_session_id value of the JSON refresh.
const sessionFile = ".tripit-session"

// defaultLookbackDays is the value of TRIPIT_JSON_REFRESH_LOOKBACK_DAYS when
// it is empty.
const defaultLookbackDays = 7

// refreshConfig is the JSON refresh setting of a scheduled run.
type refreshConfig struct {
	enabled      bool
	lookbackDays int
	sessions     []sessionValue
}

// sessionValue is one it_session_id value, and the name of the place that
// it came from, for the progress lines.
type sessionValue struct {
	source string
	value  string
}

// readRefreshConfig reads TRIPIT_JSON_REFRESH,
// TRIPIT_JSON_REFRESH_LOOKBACK_DAYS and the session values. When the refresh is off, it reads nothing else. An
// error stops the run before any request.
func readRefreshConfig(env map[string]string, outputDir string) (refreshConfig, error) {
	cfg := refreshConfig{enabled: boolEnv(env["TRIPIT_JSON_REFRESH"]), lookbackDays: defaultLookbackDays}
	if !cfg.enabled {
		return cfg, nil
	}
	if v := env["TRIPIT_JSON_REFRESH_LOOKBACK_DAYS"]; v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			return cfg, fmt.Errorf("TRIPIT_JSON_REFRESH_LOOKBACK_DAYS must be a whole number of 0 or more, not %q", v)
		}
		cfg.lookbackDays = days
	}
	cfg.sessions = sessionValues(env, outputDir)
	if len(cfg.sessions) == 0 {
		return cfg, errors.New("TRIPIT_SESSION is required when TRIPIT_JSON_REFRESH is true")
	}
	return cfg, nil
}

// sessionValues returns the it_session_id values to try, in order: the
// state file, then TRIPIT_SESSION. A value can start with "it_session_id=".
// It skips an empty value, and a value that is the same as the one before.
func sessionValues(env map[string]string, outputDir string) []sessionValue {
	var candidates []sessionValue
	if b, err := os.ReadFile(filepath.Join(outputDir, sessionFile)); err == nil {
		candidates = append(candidates, sessionValue{source: "the saved session", value: string(b)})
	}
	candidates = append(candidates, sessionValue{source: "TRIPIT_SESSION", value: secret.Read("tripit_session", "TRIPIT_SESSION", env)})

	var out []sessionValue
	for _, c := range candidates {
		c.value = strings.TrimPrefix(strings.TrimSpace(c.value), "it_session_id=")
		if c.value == "" || (len(out) > 0 && out[len(out)-1].value == c.value) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// runRefresh reads the JSON of each trip that the refresh selects, after
// the feed merge. It opens a session with each value of cfg.sessions in
// turn, and it keeps the newest it_session_id in the state file, because
// TripIt gives a new value with 15 more days to each new session. See
// docs/plan.md for the exit codes.
func runRefresh(env map[string]string, outputDir string, cfg refreshConfig, stdout, stderr io.Writer, now time.Time) int {
	ctx := context.Background()

	_, _ = fmt.Fprintln(stdout, "tripit-exporter: checking the TripIt session")
	var client *tripitweb.Client
	for _, s := range cfg.sessions {
		c := newWebClient(env, "it_session_id="+s.value, stdout)
		err := c.Profile(ctx)
		if err == nil {
			client = c
			break
		}
		if !rejectedSession(err) {
			return backfillExitCode(err, stderr)
		}
		_, _ = fmt.Fprintf(stdout, "tripit-exporter: TripIt rejected %s\n", s.source)
	}
	if client == nil {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: TripIt rejected each session value; copy a new it_session_id into TRIPIT_SESSION")
		return 1
	}
	if err := saveSession(outputDir, client); err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}

	// The feed holds the events of each trip that ended in the last 83 days,
	// so the refresh reads the JSON alone for a trip that holds events.
	cutoff := now.AddDate(0, 0, -cfg.lookbackDays).Format("2006-01-02")
	code := syncTrips(ctx, client, outputDir, "refresh", func(trip *archive.Trip) bool {
		return needsBackfill(trip) || trip.End == "" || trip.End >= cutoff
	}, stdout, stderr)

	if err := saveSession(outputDir, client); err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}
	return code
}

// rejectedSession reports whether err means that TripIt did not accept the
// it_session_id value: a 401 after the retry, or a 500, which TripIt returns
// for a value that it does not accept.
func rejectedSession(err error) bool {
	var authErr *tripitweb.AuthError
	var statusErr *tripitweb.StatusError
	return errors.As(err, &authErr) || (errors.As(err, &statusErr) && statusErr.Status == http.StatusInternalServerError)
}

// saveSession writes the current it_session_id of client to the state file,
// when the value differs from the file. os.CreateTemp makes the temporary
// file with mode 0600, and os.Rename replaces the state file whole, so a
// crash leaves the old value. No error message holds the value.
func saveSession(outputDir string, client *tripitweb.Client) error {
	value := client.CookieValue("it_session_id")
	path := filepath.Join(outputDir, sessionFile)
	if old, err := os.ReadFile(path); value == "" || (err == nil && strings.TrimSpace(string(old)) == value) {
		return nil
	}

	tmp, err := os.CreateTemp(outputDir, sessionFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("save the session: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save the session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save the session: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save the session: %w", err)
	}
	return nil
}
