package archive

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/ics"
)

func tripEventICS(uuid, tripID, start, end, dtstamp string) string {
	return "BEGIN:VEVENT\r\n" +
		"UID:" + uuid + "@tripit.com\r\n" +
		"DTSTART;VALUE=DATE:" + start + "\r\n" +
		"DTEND;VALUE=DATE:" + end + "\r\n" +
		"DTSTAMP:" + dtstamp + "\r\n" +
		"SUMMARY:Seattle, WA\r\n" +
		"DESCRIPTION:Traveler. https://www.tripit.com/trip/show?id=" + tripID + "\r\n" +
		"END:VEVENT\r\n"
}

func planEventICS(uid, tripID, dtstart, dtend, dtstamp, summary string) string {
	return "BEGIN:VEVENT\r\n" +
		"UID:item-" + uid + "@tripit.com\r\n" +
		"DTSTART:" + dtstart + "\r\n" +
		"DTEND:" + dtend + "\r\n" +
		"DTSTAMP:" + dtstamp + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DESCRIPTION:https://www.tripit.com/trip/show/id/" + tripID + " [Flight]\r\n" +
		"END:VEVENT\r\n"
}

func calendar(events ...string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n")
}

func mustReadTrip(t *testing.T, dir, uuid string) *Trip {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "trips", uuid+".json"))
	if err != nil {
		t.Fatalf("read trip file: %v", err)
	}
	var trip Trip
	if err := json.Unmarshal(data, &trip); err != nil {
		t.Fatalf("parse trip file: %v", err)
	}
	return &trip
}

func TestRunSecondFetchWithOnlyDTStampChangeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	cal := calendar(tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T000000Z"))
	if _, err := Run(dir, cal, now); err != nil {
		t.Fatalf("first run: %v", err)
	}

	tripPath := filepath.Join(dir, "trips", "trip-1.json")
	before, err := os.Stat(tripPath)
	if err != nil {
		t.Fatal(err)
	}
	icsPath := filepath.Join(dir, "trips", "trip-1.ics")
	beforeICS, err := os.Stat(icsPath)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)

	cal2 := calendar(tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T010000Z"))
	if _, err := Run(dir, cal2, now); err != nil {
		t.Fatalf("second run: %v", err)
	}

	after, err := os.Stat(tripPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("trip file was rewritten: modtime %v != %v", after.ModTime(), before.ModTime())
	}
	afterICS, err := os.Stat(icsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterICS.ModTime().Equal(beforeICS.ModTime()) {
		t.Fatalf("trip ics file was rewritten: modtime %v != %v", afterICS.ModTime(), beforeICS.ModTime())
	}
}

func TestRunPlanAbsentFromTripIsDeleted(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	cal := calendar(
		tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T000000Z"),
		planEventICS("plan-a", "111", "20260615T120000Z", "20260615T140000Z", "20260620T000000Z", "AS123 SEA to LAX"),
		planEventICS("plan-b", "111", "20260616T120000Z", "20260616T140000Z", "20260620T000000Z", "Check-in: Hotel"),
	)
	if _, err := Run(dir, cal, now); err != nil {
		t.Fatal(err)
	}
	trip := mustReadTrip(t, dir, "trip-1")
	if len(trip.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(trip.Events))
	}

	cal2 := calendar(
		tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T100000Z"),
		planEventICS("plan-a", "111", "20260615T120000Z", "20260615T140000Z", "20260620T100000Z", "AS123 SEA to LAX"),
	)
	if _, err := Run(dir, cal2, now); err != nil {
		t.Fatal(err)
	}
	trip = mustReadTrip(t, dir, "trip-1")
	if len(trip.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2 after plan-b left the feed", len(trip.Events))
	}
	for _, e := range trip.Events {
		if e.UID == "item-plan-b@tripit.com" {
			t.Fatal("plan-b is still in the archive")
		}
	}
}

func TestMergeWindowRules(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		daysSince int
		inFeed    bool
		wantKept  bool
	}{
		{"endedOver97DaysAgoStays", 120, true, true},
		{"endedUnder83DaysAgoIsDeleted", 50, true, false},
		{"edgeOfWindowStays", 90, true, true},
		{"notInFeedStays", 50, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end := now.AddDate(0, 0, -tt.daysSince)
			trips := map[string]*Trip{
				"absent-trip": {
					Schema: 1,
					UUID:   "absent-trip",
					TripID: "999",
					Start:  end.AddDate(0, 0, -4).Format("2006-01-02"),
					End:    end.Format("2006-01-02"),
					InFeed: tt.inFeed,
					Events: []Event{{UID: "absent-trip@tripit.com", ICS: tripEventICS("absent-trip", "999", "20260101", "20260102", "20260101T000000Z")}},
				},
			}

			// The fetch holds an unrelated trip, so "absent-trip" is absent
			// from it.
			events, err := ics.ParseEvents(calendar(tripEventICS("other-trip", "1", "20260615", "20260619", "20260620T000000Z")))
			if err != nil {
				t.Fatal(err)
			}

			Merge(trips, events, now)

			_, kept := trips["absent-trip"]
			if kept != tt.wantKept {
				t.Fatalf("kept = %v, want %v", kept, tt.wantKept)
			}
		})
	}
}

func TestRunKeepsV2FieldWhenTripIsRewritten(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	cal := calendar(tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T000000Z"))
	if _, err := Run(dir, cal, now); err != nil {
		t.Fatal(err)
	}

	trips, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	trips["trip-1"].V2 = json.RawMessage(`{"uuid":"trip-1","last_modified":"1"}`)
	if err := Write(dir, trips); err != nil {
		t.Fatal(err)
	}

	// A later fetch changes a plan under the same trip.
	cal2 := calendar(
		tripEventICS("trip-1", "111", "20260615", "20260619", "20260620T100000Z"),
		planEventICS("plan-a", "111", "20260615T120000Z", "20260615T140000Z", "20260620T100000Z", "AS123 SEA to LAX"),
	)
	if _, err := Run(dir, cal2, now); err != nil {
		t.Fatal(err)
	}

	trip := mustReadTrip(t, dir, "trip-1")
	var v2 map[string]string
	if err := json.Unmarshal(trip.V2, &v2); err != nil {
		t.Fatal(err)
	}
	if v2["uuid"] != "trip-1" || v2["last_modified"] != "1" {
		t.Fatalf("V2 = %s, want it unchanged", trip.V2)
	}
}

func TestMergeWarnsOnOrphanPlan(t *testing.T) {
	trips := map[string]*Trip{}
	events, err := ics.ParseEvents(calendar(
		planEventICS("plan-a", "999", "20260615T120000Z", "20260615T140000Z", "20260620T000000Z", "AS123 SEA to LAX"),
	))
	if err != nil {
		t.Fatal(err)
	}

	warnings := Merge(trips, events, time.Now())
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
	if len(trips) != 0 {
		t.Fatalf("len(trips) = %d, want 0: an orphan plan must not be written", len(trips))
	}
}

func TestRunOrdersTripsByStartThenUUID(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	// trip-b starts before trip-a, so it must come first in tripit.ics even
	// though its UUID sorts after.
	cal := calendar(
		tripEventICS("trip-a", "111", "20260701", "20260705", "20260620T000000Z"),
		tripEventICS("trip-b", "222", "20260615", "20260619", "20260620T000000Z"),
	)
	if _, err := Run(dir, cal, now); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "tripit.ics"))
	if err != nil {
		t.Fatal(err)
	}

	indexB := strings.Index(string(data), "trip-b@tripit.com")
	indexA := strings.Index(string(data), "trip-a@tripit.com")
	if indexB == -1 || indexA == -1 {
		t.Fatalf("tripit.ics missing an event: %s", data)
	}
	if indexB > indexA {
		t.Fatalf("trip-b (starts earlier) must come before trip-a in tripit.ics")
	}
}

func TestAtomicWriteFailureLeavesOldFileWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trip-1.json")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := atomicWrite(dir, "trip-1.json", func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want an error from a failing render func")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old content" {
		t.Fatalf("file content = %q, want %q", got, "old content")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (the temp file must be removed)", len(entries))
	}
}
