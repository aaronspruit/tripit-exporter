package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

const testCookie = "JSESSIONID=super-secret-session-value"

func tripJSON(uuid string) string {
	return `{"uuid":"` + uuid + `","relative_url":"/trip/show/id/1001"}`
}

func tripDetailJSON(uuid string) string {
	return `{"Trip":{"uuid":"` + uuid + `","relative_url":"/trip/show/id/1001","start_date":"2026-01-10","end_date":"2026-01-15"}}`
}

const tripCalendar = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:trip-a@tripit.com\r\n" +
	"DTSTART;VALUE=DATE:20260110\r\n" +
	"DTEND;VALUE=DATE:20260116\r\n" +
	"SUMMARY:Denver, CO\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func newBackfillServer(t *testing.T, uuids ...string) *tripittest.Server {
	t.Helper()
	s := tripittest.New()

	var trips []json.RawMessage
	for _, uuid := range uuids {
		trips = append(trips, json.RawMessage(tripJSON(uuid)))
		s.SetTripDetail(uuid, tripDetailJSON(uuid))
		s.SetDownload(uuid, tripCalendar)
	}
	s.SetTrips(trips)
	return s
}

func TestBackfillWritesTripFile(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "trips", "trip-a.json"))
	if err != nil {
		t.Fatalf("trip file not written: %v", err)
	}
	var trip archive.Trip
	if err := json.Unmarshal(data, &trip); err != nil {
		t.Fatalf("parse trip file: %v", err)
	}
	if trip.InFeed {
		t.Fatal("a backfilled trip must have in_feed: false")
	}
	if len(trip.V2) == 0 || string(trip.V2) == "null" {
		t.Fatal("the trip file must hold the v2 object")
	}
	if len(trip.Events) != 1 {
		t.Fatalf("trip has %d events, want 1 from the downloaded calendar", len(trip.Events))
	}
}

func TestBackfillNoOutputHoldsTheCookie(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if strings.Contains(stdout.String(), testCookie) {
		t.Fatalf("stdout holds the cookie: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), testCookie) {
		t.Fatalf("stderr holds the cookie: %q", stderr.String())
	}

	files, _ := filepath.Glob(filepath.Join(dir, "trips", "*"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), testCookie) {
			t.Fatalf("%s holds the cookie", f)
		}
	}
}

func TestBackfillSecondRunSkipsTripsThatHaveV2(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()

	dir := t.TempDir()
	env := map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("first run: exit code = %d, stderr = %q", code, stderr.String())
	}
	before, err := os.ReadFile(filepath.Join(dir, "trips", "trip-a.json"))
	if err != nil {
		t.Fatal(err)
	}

	// The second run must not call the detail or download route again: the
	// fake server holds no response for a second call once cleared, so a
	// second read would fail the run instead of silently reusing the value.
	s.SetTripDetail("trip-a", "")
	s.SetDownload("trip-a", "")

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("second run: exit code = %d, stderr = %q", code, stderr.String())
	}

	after, err := os.ReadFile(filepath.Join(dir, "trips", "trip-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a second run touched a trip that already had v2")
	}
}

func TestBackfillRateLimitExitsZeroWithTripsBeforeItOnDisk(t *testing.T) {
	s := newBackfillServer(t, "trip-a", "trip-b")
	defer s.Close()
	s.RateLimitTripDetail("trip-b")

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "trip-a.json")); err != nil {
		t.Fatalf("trip-a must be on disk before the run stopped on trip-b: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "trip-b.json")); !os.IsNotExist(err) {
		t.Fatalf("trip-b must not be on disk: err = %v", err)
	}
}

func TestBackfillUnauthorizedBeforeAnyTripRequestExitsOne(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()
	s.SetUnauthorizedCount(2)

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "trips")); !os.IsNotExist(err) {
		t.Fatal("an unauthorized profile call must not touch the archive")
	}
}

func TestBackfilledTripStaysAfterFeedRunThatDoesNotHoldIt(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("backfill: exit code = %d, stderr = %q", code, stderr.String())
	}

	feed := tripittest.New()
	defer feed.Close()
	feed.SetFeed(http.StatusOK, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")

	stdout.Reset()
	stderr.Reset()
	code := run(nil, map[string]string{"TRIPIT_FEED_URL": feed.FeedURL("key"), "OUTPUT_DIR": dir},
		strings.NewReader(""), &stdout, &stderr, testNow)
	if code != 0 {
		t.Fatalf("feed run: exit code = %d, stderr = %q", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "trips", "trip-a.json")); err != nil {
		t.Fatalf("the backfilled trip must stay: %v", err)
	}
}

func TestBackfillMissingOutputDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, nil, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "OUTPUT_DIR") {
		t.Fatalf("stderr = %q, want it to name OUTPUT_DIR", stderr.String())
	}
}

func TestReadCookieEmptyIsError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": t.TempDir()},
		strings.NewReader("\n"), &stdout, &stderr, testNow)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
