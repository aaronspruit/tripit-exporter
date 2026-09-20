package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/airtrail"
	"github.com/aaronspruit/tripit-exporter/internal/airtrailtest"
	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

// airtrailTripDetail is the v2 object of one trip with one flight.
const airtrailTripDetail = `{"Trip":{"uuid":"trip-1"},"AirObject":{"is_client_traveler":"true","Segment":{
  "uuid":"seg-1",
  "start_airport_code":"SEA","end_airport_code":"PDX",
  "StartDateTime":{"date":"2026-06-15","time":"08:00:00","utc_offset":"-07:00"},
  "EndDateTime":{"date":"2026-06-15","time":"08:55:00","utc_offset":"-07:00"},
  "marketing_airline":"Alaska Airlines","marketing_airline_code":"AS",
  "marketing_flight_number":"2040","aircraft":"73H"
}}}`

// newAirtrailRun starts a fake TripIt feed and a fake AirTrail, writes one
// backfilled trip into the archive, and returns the environment of a run
// with the sync on.
func newAirtrailRun(t *testing.T) (*airtrailtest.Server, string, map[string]string) {
	t.Helper()
	tripit := tripittest.New()
	t.Cleanup(tripit.Close)
	tripit.SetFeed(http.StatusOK, testCalendar)

	airtrailServer := airtrailtest.New()
	t.Cleanup(airtrailServer.Close)

	dir := t.TempDir()
	trips := map[string]*archive.Trip{"trip-1": {
		Schema: 1, UUID: "trip-1", Start: "2026-06-15", End: "2026-06-19", InFeed: true,
		V2: json.RawMessage(airtrailTripDetail), EmptyDownload: true,
	}}
	if err := archive.Write(dir, trips); err != nil {
		t.Fatalf("write the archive: %v", err)
	}

	return airtrailServer, dir, map[string]string{
		"TRIPIT_FEED_URL":         tripit.FeedURL("key"),
		"OUTPUT_DIR":              dir,
		"TRIPIT_AIRTRAIL_SYNC":    "true",
		"TRIPIT_AIRTRAIL_URL":     airtrailServer.URL,
		"TRIPIT_AIRTRAIL_API_KEY": airtrailtest.APIKey,
	}
}

func TestRunFeedSyncsToAirtrail(t *testing.T) {
	server, dir, env := newAirtrailRun(t)
	var stdout, stderr bytes.Buffer

	code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	flights := server.Flights()
	if len(flights) != 1 {
		t.Fatalf("got %d flights in AirTrail, want 1", len(flights))
	}
	if flights[0]["from"] != "SEA" || flights[0]["airline"] != "ASA" || flights[0]["aircraft"] != "B738" {
		t.Errorf("flight = %v, want SEA with the ICAO codes", flights[0])
	}
	if _, err := os.Stat(filepath.Join(dir, airtrail.StateFile)); err != nil {
		t.Errorf("the state file is missing: %v", err)
	}
}

func TestRunFeedSkipsAirtrailWhenTheSyncIsOff(t *testing.T) {
	server, dir, env := newAirtrailRun(t)
	env["TRIPIT_AIRTRAIL_SYNC"] = "false"
	var stdout, stderr bytes.Buffer

	if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if saves, _ := server.Counts(); saves != 0 {
		t.Errorf("got %d saves, want none", saves)
	}
	if _, err := os.Stat(filepath.Join(dir, airtrail.StateFile)); !os.IsNotExist(err) {
		t.Errorf("the run wrote a state file with the sync off")
	}
}

func TestRunFeedAirtrailRejectedKey(t *testing.T) {
	server, _, env := newAirtrailRun(t)
	server.RejectKey()
	var stdout, stderr bytes.Buffer

	code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != airtrailExitCode {
		t.Fatalf("exit code = %d, want %d", code, airtrailExitCode)
	}
	if !strings.Contains(stderr.String(), "API key") {
		t.Errorf("stderr = %q, want it to name the API key", stderr.String())
	}
}

func TestReadAirtrailConfig(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"off reads nothing else", map[string]string{}, ""},
		{"a missing URL", map[string]string{"TRIPIT_AIRTRAIL_SYNC": "true"}, "TRIPIT_AIRTRAIL_URL"},
		{"a missing key", map[string]string{
			"TRIPIT_AIRTRAIL_SYNC": "true", "TRIPIT_AIRTRAIL_URL": "https://airtrail.example.com",
		}, "TRIPIT_AIRTRAIL_API_KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := readAirtrailConfig(c.env)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("got %v, want no error", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("got %v, want an error naming %s", err, c.wantErr)
			}
		})
	}
}

func TestReadAirtrailConfigDefaults(t *testing.T) {
	cfg, err := readAirtrailConfig(map[string]string{
		"TRIPIT_AIRTRAIL_SYNC":    "true",
		"TRIPIT_AIRTRAIL_URL":     "https://airtrail.example.com",
		"TRIPIT_AIRTRAIL_API_KEY": "key",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !cfg.delete {
		t.Errorf("delete is off by default, want it on")
	}
	if cfg.userID != "" {
		t.Errorf("userID = %q, want it empty so the placeholder is used", cfg.userID)
	}
}

func TestReadAirtrailConfigTurnsDeletesOff(t *testing.T) {
	cfg, err := readAirtrailConfig(map[string]string{
		"TRIPIT_AIRTRAIL_SYNC":    "true",
		"TRIPIT_AIRTRAIL_URL":     "https://airtrail.example.com",
		"TRIPIT_AIRTRAIL_API_KEY": "key",
		"TRIPIT_AIRTRAIL_DELETE":  "false",
		"TRIPIT_AIRTRAIL_USER_ID": "aaron",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if cfg.delete {
		t.Errorf("delete is on, want it off")
	}
	if cfg.userID != "aaron" {
		t.Errorf("userID = %q, want aaron", cfg.userID)
	}
}
