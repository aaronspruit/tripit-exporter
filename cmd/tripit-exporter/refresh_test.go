package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

const testSession = "remembered-secret-value"

// newRefreshServer starts a fake TripIt server that serves testCalendar as
// the feed and no trips.
func newRefreshServer(t *testing.T) *tripittest.Server {
	t.Helper()
	backfillSleep = func(time.Duration) {}
	t.Cleanup(func() { backfillSleep = nil })
	s := tripittest.New()
	t.Cleanup(s.Close)
	s.SetFeed(http.StatusOK, testCalendar)
	return s
}

// refreshEnv returns the environment of a scheduled run against s, with the
// JSON refresh on.
func refreshEnv(s *tripittest.Server, dir string) map[string]string {
	return map[string]string{
		"TRIPIT_FEED_URL":     s.FeedURL("key"),
		"OUTPUT_DIR":          dir,
		"TRIPIT_WEB_BASE_URL": s.URL,
		"TRIPIT_JSON_REFRESH": "true",
		"TRIPIT_SESSION":      testSession,
	}
}

func datedDetailJSON(uuid, start, end, timestamp string) string {
	return `{"timestamp":"` + timestamp + `","Trip":{"uuid":"` + uuid + `","start_date":"` + start + `","end_date":"` + end + `"}}`
}

// archiveTrips writes one backfilled trip file for each uuid, with the v2
// object of datedDetailJSON and an empty download, so that only the refresh
// window selects the trip.
func archiveTrips(t *testing.T, dir string, ends map[string]string) {
	t.Helper()
	trips := make(map[string]*archive.Trip, len(ends))
	for uuid, end := range ends {
		trips[uuid] = &archive.Trip{
			Schema:        1,
			UUID:          uuid,
			Start:         end,
			End:           end,
			V2:            json.RawMessage(datedDetailJSON(uuid, end, end, "1")),
			EmptyDownload: true,
		}
	}
	if err := archive.Write(dir, trips); err != nil {
		t.Fatal(err)
	}
}

func setListedTrips(s *tripittest.Server, uuids ...string) {
	var trips []json.RawMessage
	for _, uuid := range uuids {
		trips = append(trips, json.RawMessage(tripJSON(uuid)))
	}
	s.SetTrips(trips)
}

// detailRequests returns the trip uuid of each detail request that s
// received.
func detailRequests(s *tripittest.Server) []string {
	var out []string
	for _, p := range s.Paths() {
		if rest, ok := strings.CutPrefix(p, "/api/v2/get/trip/uuid/"); ok {
			out = append(out, strings.SplitN(rest, "/", 2)[0])
		}
	}
	slices.Sort(out)
	return out
}

func readSessionFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	return string(data)
}

func TestRefreshIsOffByDefault(t *testing.T) {
	s := newRefreshServer(t)
	env := refreshEnv(s, t.TempDir())
	delete(env, "TRIPIT_JSON_REFRESH")

	var stdout, stderr bytes.Buffer
	if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	for _, p := range s.Paths() {
		if strings.HasPrefix(p, "/api/v2/") {
			t.Fatalf("the run sent %s with the refresh off", p)
		}
	}
}

func TestRefreshSettingErrorsExitTwoBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name   string
		change func(env map[string]string)
		want   string
	}{
		{"no session", func(env map[string]string) { delete(env, "TRIPIT_SESSION") }, "TRIPIT_SESSION"},
		{"negative days", func(env map[string]string) { env["TRIPIT_JSON_REFRESH_LOOKBACK_DAYS"] = "-1" }, "TRIPIT_JSON_REFRESH_LOOKBACK_DAYS"},
		{"days not a number", func(env map[string]string) { env["TRIPIT_JSON_REFRESH_LOOKBACK_DAYS"] = "seven" }, "TRIPIT_JSON_REFRESH_LOOKBACK_DAYS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRefreshServer(t)
			env := refreshEnv(s, t.TempDir())
			tt.change(env)

			var stdout, stderr bytes.Buffer
			if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want it to name %s", stderr.String(), tt.want)
			}
			if paths := s.Paths(); len(paths) != 0 {
				t.Fatalf("the run sent %v, want no request", paths)
			}
		})
	}
}

func TestRefreshUsesTheSavedSessionFirstAndSavesTheNewValue(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte("saved-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if got, want := readSessionFile(t, dir), tripittest.RenewedSession("saved-value")+"\n"; got != want {
		t.Fatalf("state file = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, sessionFile))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state file mode = %o, want 600", mode)
	}
}

func TestRefreshRejectedSavedSessionFallsBackToTripitSession(t *testing.T) {
	s := newRefreshServer(t)
	s.RejectSession("saved-value")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte("saved-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if got, want := readSessionFile(t, dir), tripittest.RenewedSession(testSession)+"\n"; got != want {
		t.Fatalf("state file = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "TripIt rejected the saved session") {
		t.Fatalf("stdout = %q, want the rejected line", stdout.String())
	}
}

func TestRefreshSavedSessionWithTwo401sFallsBackToTripitSession(t *testing.T) {
	s := newRefreshServer(t)
	// The profile request of the saved value gets a 401, and the one retry
	// gets the second 401, so the value becomes an *AuthError.
	s.SetUnauthorizedCount(2)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte("saved-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if got, want := readSessionFile(t, dir), tripittest.RenewedSession(testSession)+"\n"; got != want {
		t.Fatalf("state file = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "TripIt rejected the saved session") {
		t.Fatalf("stdout = %q, want the rejected line", stdout.String())
	}
}

func TestRefreshProfileErrorThatIsNotARejectionStopsAtTheFirstValue(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   int
	}{
		{"rate limit", http.StatusTooManyRequests, 0},
		{"other status", http.StatusServiceUnavailable, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRefreshServer(t)
			s.SetProfileStatus(tt.status)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte("saved-value\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != tt.want {
				t.Fatalf("exit code = %d, want %d, stderr = %q", code, tt.want, stderr.String())
			}

			// The error is not a rejection, so the run keeps the saved value
			// and sends no request with TRIPIT_SESSION.
			if got, want := readSessionFile(t, dir), "saved-value\n"; got != want {
				t.Fatalf("state file = %q, want %q", got, want)
			}
			if strings.Contains(stdout.String(), "TripIt rejected") {
				t.Fatalf("stdout = %q, want no rejected line", stdout.String())
			}
			for _, p := range s.Paths() {
				if p == "/api/v2/list/trip" {
					t.Fatal("the run listed the trips after a profile error")
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "trips", "trip-1.json")); err != nil {
				t.Fatalf("the feed merge must stay on disk: %v", err)
			}
		})
	}
}

func TestRefreshEveryRejectedSessionExitsOneAndKeepsTheFeedMerge(t *testing.T) {
	s := newRefreshServer(t)
	s.RejectSession(testSession)
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 1 {
		t.Fatalf("exit code = %d, want 1, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "TRIPIT_SESSION") {
		t.Fatalf("stderr = %q, want it to name TRIPIT_SESSION", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "trip-1.json")); err != nil {
		t.Fatalf("the feed merge must stay on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionFile)); !os.IsNotExist(err) {
		t.Fatalf("a rejected session must write no state file: err = %v", err)
	}
}

func TestRefreshReadsTheTripsInTheWindowAndEachNewTrip(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	// testNow is 2026-06-20, so the 7-day window starts on 2026-06-13.
	archiveTrips(t, dir, map[string]string{
		"trip-old":     "2026-01-15",
		"trip-edge":    "2026-06-12",
		"trip-recent":  "2026-06-13",
		"trip-future":  "2026-08-05",
		"trip-no-date": "",
	})
	setListedTrips(s, "trip-old", "trip-edge", "trip-recent", "trip-future", "trip-no-date", "trip-new")
	for uuid, end := range map[string]string{"trip-old": "2026-01-15", "trip-edge": "2026-06-12", "trip-future": "2026-08-05", "trip-no-date": ""} {
		s.SetTripDetail(uuid, datedDetailJSON(uuid, end, end, "2"))
	}
	s.SetTripDetail("trip-recent", `{"timestamp":"2","Trip":{"uuid":"trip-recent","start_date":"2026-06-10","end_date":"2026-06-13","display_name":"changed"}}`)
	s.SetTripDetail("trip-new", datedDetailJSON("trip-new", "2025-03-01", "2025-03-04", "2"))
	s.SetDownload("trip-new", tripCalendar)

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	want := []string{"trip-future", "trip-new", "trip-no-date", "trip-recent"}
	if got := detailRequests(s); !slices.Equal(got, want) {
		t.Fatalf("detail requests = %v, want %v", got, want)
	}
	if recent := readTripFile(t, dir, "trip-recent"); !strings.Contains(string(recent.V2), "changed") {
		t.Fatalf("trip-recent v2 = %s, want the changed detail", recent.V2)
	}
	if created := readTripFile(t, dir, "trip-new"); len(created.Events) != 1 || created.End != "2025-03-04" {
		t.Fatalf("trip-new = %+v, want the full backfill with its downloaded event", created)
	}
	if !strings.Contains(stdout.String(), "6 trips, 4 to refresh") {
		t.Fatalf("stdout = %q, want the refresh count", stdout.String())
	}
}

func TestRefreshDetailThatDiffersOnlyInTimestampWritesNoFile(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	archiveTrips(t, dir, map[string]string{"trip-future": "2026-08-05"})
	setListedTrips(s, "trip-future")
	s.SetTripDetail("trip-future", datedDetailJSON("trip-future", "2026-08-05", "2026-08-05", "2"))

	path := filepath.Join(dir, "trips", "trip-future.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if got := detailRequests(s); !slices.Equal(got, []string{"trip-future"}) {
		t.Fatalf("detail requests = %v, want trip-future", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a timestamp change rewrote the trip file:\nbefore %s\nafter  %s", before, after)
	}
}

func TestRefreshRateLimitExitsZeroAndSavesTheSession(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	archiveTrips(t, dir, map[string]string{"trip-future": "2026-08-05"})
	setListedTrips(s, "trip-future")
	s.RateLimitTripDetail("trip-future")

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if got, want := readSessionFile(t, dir), tripittest.RenewedSession(testSession)+"\n"; got != want {
		t.Fatalf("state file = %q, want %q", got, want)
	}
}

func TestRefreshNoOutputOrArchiveFileHoldsTheSession(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	setListedTrips(s, "trip-new")
	s.SetTripDetail("trip-new", datedDetailJSON("trip-new", "2026-08-01", "2026-08-05", "2"))
	s.SetDownload("trip-new", tripCalendar)
	env := refreshEnv(s, dir)
	env["TRIPIT_VERBOSE"] = "true"

	var stdout, stderr bytes.Buffer
	if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if strings.Contains(stdout.String()+stderr.String(), testSession) {
		t.Fatal("the output holds the session value")
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), testSession) && d.Name() != sessionFile {
			t.Errorf("%s holds the session value", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionValues(t *testing.T) {
	tests := []struct {
		name  string
		saved string
		env   string
		want  []string
	}{
		{"saved then env", "saved\n", "env", []string{"saved", "env"}},
		{"the same value once", "same\n", "same", []string{"same"}},
		{"cookie prefix and spaces", "", " it_session_id=value ", []string{"value"}},
		{"nothing", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.saved != "" {
				if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte(tt.saved), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var got []string
			for _, v := range sessionValues(map[string]string{"TRIPIT_SESSION": tt.env}, dir) {
				got = append(got, v.value)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("sessionValues() = %q, want %q", got, tt.want)
			}
		})
	}
}

// tripFileExists reports whether the archive holds both files of uuid.
func tripFileExists(t *testing.T, dir, uuid string) bool {
	t.Helper()
	_, jsonErr := os.Stat(filepath.Join(dir, "trips", uuid+".json"))
	_, icsErr := os.Stat(filepath.Join(dir, "trips", uuid+".ics"))
	if os.IsNotExist(jsonErr) && os.IsNotExist(icsErr) {
		return false
	}
	if jsonErr != nil || icsErr != nil {
		t.Fatalf("trip %s: json err = %v, ics err = %v", uuid, jsonErr, icsErr)
	}
	return true
}

func TestRefreshDeletesEachPastTripThatTripItNoLongerHolds(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	// testNow is 2026-06-20. trip-1 comes from testCalendar and ends
	// 2026-06-19, inside the feed window.
	archiveTrips(t, dir, map[string]string{
		"trip-kept":   "2026-01-11",
		"trip-gone":   "2026-01-10",
		"trip-coming": "2026-12-01",
	})
	setListedTrips(s, "trip-kept")

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}

	if tripFileExists(t, dir, "trip-gone") {
		t.Fatal("trip-gone is absent from the trip list, so its files must be gone")
	}
	for _, uuid := range []string{"trip-kept", "trip-coming", "trip-1"} {
		if !tripFileExists(t, dir, uuid) {
			t.Fatalf("trip %s must stay in the archive", uuid)
		}
	}
	if !strings.Contains(stdout.String(), "1 trips, 0 to refresh, 1 deleted at TripIt") {
		t.Fatalf("stdout = %q, want the deleted count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "trip trip-gone is gone from TripIt") {
		t.Fatalf("stdout = %q, want the line that names the deleted trip", stdout.String())
	}
}

func TestRefreshEmptyTripListDeletesNothing(t *testing.T) {
	s := newRefreshServer(t)
	dir := t.TempDir()
	archiveTrips(t, dir, map[string]string{"trip-old": "2026-01-10"})
	s.SetTrips(nil)

	var stdout, stderr bytes.Buffer
	if code := run(nil, refreshEnv(s, dir), strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if !tripFileExists(t, dir, "trip-old") {
		t.Fatal("an empty trip list must delete nothing")
	}
}
