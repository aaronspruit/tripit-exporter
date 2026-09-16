package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

const testCookie = "JSESSIONID=super-secret-session-value"

func tripJSON(uuid string) string {
	return `{"uuid":"` + uuid + `","relative_url":"/trip/show/uuid/` + uuid + `"}`
}

func tripDetailJSON(uuid string) string {
	return `{"Trip":{"uuid":"` + uuid + `","relative_url":"/trip/show/uuid/` + uuid + `","start_date":"2026-01-10","end_date":"2026-01-15"}}`
}

const tripCalendar = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:trip-a@tripit.com\r\n" +
	"DTSTART;VALUE=DATE:20260110\r\n" +
	"DTEND;VALUE=DATE:20260116\r\n" +
	"SUMMARY:Denver, CO\r\n" +
	"DESCRIPTION:View and/or edit details in TripIt : https://www.tripit.com/trip/show?id=1001\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func newBackfillServer(t *testing.T, uuids ...string) *tripittest.Server {
	t.Helper()
	backfillSleep = func(time.Duration) {}
	t.Cleanup(func() { backfillSleep = nil })
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
	if trip.TripID != "1001" {
		t.Fatalf("trip_id = %q, want 1001 from the link in the downloaded trip event", trip.TripID)
	}

	for _, line := range []string{"checking the cookie", "listing the trips", "1 trips, 1 to backfill", "trip 1 of 1: trip-a", "done"} {
		if !strings.Contains(stdout.String(), line) {
			t.Fatalf("stdout = %q, want the progress line %q", stdout.String(), line)
		}
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

func TestBackfillSecondRunSkipsTripsThatHaveV2AndEvents(t *testing.T) {
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

func readTripFile(t *testing.T, dir, uuid string) archive.Trip {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "trips", uuid+".json"))
	if err != nil {
		t.Fatalf("trip file %s not written: %v", uuid, err)
	}
	var trip archive.Trip
	if err := json.Unmarshal(data, &trip); err != nil {
		t.Fatalf("parse trip file %s: %v", uuid, err)
	}
	return trip
}

func TestBackfillBlockedDownloadKeepsV2AndContinues(t *testing.T) {
	s := newBackfillServer(t, "trip-a", "trip-b")
	defer s.Close()
	s.BlockDownload("trip-a")

	dir := t.TempDir()
	env := map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL}
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "download trip trip-a: unexpected status 403") {
		t.Fatalf("stderr = %q, want a warning for the blocked download", stderr.String())
	}
	blocked := readTripFile(t, dir, "trip-a")
	if !hasV2(&blocked) || len(blocked.Events) != 0 || blocked.EmptyDownload {
		t.Fatalf("trip-a: v2 = %s, %d events, empty_download = %v, want the v2 object, no events and no empty download", blocked.V2, len(blocked.Events), blocked.EmptyDownload)
	}
	if next := readTripFile(t, dir, "trip-b"); len(next.Events) != 1 {
		t.Fatalf("trip-b has %d events, want 1: the run must continue after a blocked download", len(next.Events))
	}

	// The next run tries the download of trip-a again.
	s2 := newBackfillServer(t, "trip-a")
	defer s2.Close()
	env["TRIPIT_WEB_BASE_URL"] = s2.URL
	stderr.Reset()
	if code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("second run: exit code = %d, stderr = %q", code, stderr.String())
	}
	if retried := readTripFile(t, dir, "trip-a"); len(retried.Events) != 1 {
		t.Fatalf("trip-a has %d events after the second run, want 1", len(retried.Events))
	}
}

func TestBackfillRetriesTripFileWithV2AndNoEventsKey(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "trips"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := `{"schema":1,"uuid":"trip-a","events":[],"v2":` + tripDetailJSON("trip-a") + `}`
	if err := os.WriteFile(filepath.Join(dir, "trips", "trip-a.json"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if trip := readTripFile(t, dir, "trip-a"); len(trip.Events) != 1 {
		t.Fatalf("trip-a has %d events, want 1: a trip with v2, no events and no empty_download key must get the download", len(trip.Events))
	}
}

func TestBackfillUnparsableDownloadKeepsV2AndContinues(t *testing.T) {
	s := newBackfillServer(t, "trip-a", "trip-b")
	defer s.Close()
	s.SetDownload("trip-a", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x\r\nEND:VCALENDAR\r\n")

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "parse downloaded calendar for trip trip-a") {
		t.Fatalf("stderr = %q, want a warning for the calendar that does not parse", stderr.String())
	}
	if bad := readTripFile(t, dir, "trip-a"); !hasV2(&bad) || len(bad.Events) != 0 || bad.EmptyDownload {
		t.Fatalf("trip-a: %d events, empty_download = %v, want the v2 object, no events and no empty download", len(bad.Events), bad.EmptyDownload)
	}
	if next := readTripFile(t, dir, "trip-b"); len(next.Events) != 1 {
		t.Fatalf("trip-b has %d events, want 1: the run must continue after a calendar that does not parse", len(next.Events))
	}
}

func TestBackfillSecondRunSkipsTripWithZeroDownloadedEvents(t *testing.T) {
	s := newBackfillServer(t, "trip-a")
	defer s.Close()
	s.SetDownload("trip-a", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")

	dir := t.TempDir()
	env := map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("first run: exit code = %d, stderr = %q", code, stderr.String())
	}
	if trip := readTripFile(t, dir, "trip-a"); len(trip.Events) != 0 || !trip.EmptyDownload {
		t.Fatalf("trip-a: %d events, empty_download = %v, want no events and an empty download", len(trip.Events), trip.EmptyDownload)
	}

	// A second call to the detail route would get a 404 and stop the run.
	s.RateLimitTripDetail("trip-a")
	if code := run([]string{"backfill"}, env, strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("second run: exit code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "rate-limited") {
		t.Fatal("the second run requested the detail of a trip with no pending download")
	}
}

func TestBackfillSkipsUnsafeUUID(t *testing.T) {
	s := newBackfillServer(t, "../evil")
	defer s.Close()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"backfill"}, map[string]string{"OUTPUT_DIR": dir, "TRIPIT_WEB_BASE_URL": s.URL},
		strings.NewReader(testCookie+"\n"), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not safe as a file name") {
		t.Fatalf("stderr = %q, want a warning for the unsafe UUID", stderr.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "trips")); len(entries) != 0 {
		t.Fatalf("trips holds %d files, want none", len(entries))
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
