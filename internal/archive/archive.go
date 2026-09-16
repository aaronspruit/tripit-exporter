// Package archive loads, merges and writes the trip files, and makes the
// two ICS files: one for each trip, and one for every trip together.
package archive

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/ics"
)

// windowMargin is the number of days at the edge of the 90-day feed window
// where an absent trip stays in the archive, because the exact boundary day
// is not certain.
const windowMargin = 7 * 24 * time.Hour

// windowDays is the length of the TripIt feed window.
const windowDays = 90 * 24 * time.Hour

// Event is one VEVENT that belongs to a trip.
type Event struct {
	UID string `json:"uid"`
	ICS string `json:"ics"`
}

// Trip is one trip file under data/trips/<uuid>.json.
type Trip struct {
	Schema int             `json:"schema"`
	UUID   string          `json:"uuid"`
	TripID string          `json:"trip_id"`
	Start  string          `json:"start"`
	End    string          `json:"end"`
	InFeed bool            `json:"in_feed"`
	Events []Event         `json:"events"`
	V2     json.RawMessage `json:"v2"`
	// DownloadPending is true when the backfill read the v2 object but did
	// not get the events of the download, so the next backfill tries the
	// download again.
	DownloadPending bool `json:"download_pending,omitempty"`
}

// Load reads every trip file under dir/trips. A missing dir/trips is not an
// error: it means the archive is empty.
func Load(dir string) (map[string]*Trip, error) {
	trips := make(map[string]*Trip)

	entries, err := os.ReadDir(filepath.Join(dir, "trips"))
	if os.IsNotExist(err) {
		return trips, nil
	}
	if err != nil {
		return nil, fmt.Errorf("archive: read trips directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, "trips", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("archive: read %s: %w", path, err)
		}

		var trip Trip
		if err := json.Unmarshal(data, &trip); err != nil {
			return nil, fmt.Errorf("archive: parse %s: %w", path, err)
		}
		trips[trip.UUID] = &trip
	}
	return trips, nil
}

// Run applies one feed fetch to the archive in dir: it parses calendar,
// merges its events into the trips that dir/trips holds, and writes the
// files that changed. It returns a warning for each plan event whose trip
// is not in this fetch.
func Run(dir string, calendar []byte, now time.Time) ([]string, error) {
	events, err := ics.ParseEvents(calendar)
	if err != nil {
		return nil, fmt.Errorf("archive: parse calendar: %w", err)
	}

	trips, err := Load(dir)
	if err != nil {
		return nil, err
	}

	warnings := Merge(trips, events, now)

	if err := Write(dir, trips); err != nil {
		return nil, err
	}
	return warnings, nil
}

// tripIDPattern matches the numeric trip ID in the two TripIt link forms:
// trip/show?id=<id> in a trip event and trip/show/id/<id> in a plan event.
var tripIDPattern = regexp.MustCompile(`tripit\.com/trip/show(?:\?id=|/id/)(\d+)`)

// tripUUIDPattern is the set of trip UUIDs that the archive accepts. The
// UUID becomes a file name, so a UUID with a path separator or a dot must
// never reach Write.
var tripUUIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// ValidTripUUID reports whether uuid is safe as a trip file name.
func ValidTripUUID(uuid string) bool { return tripUUIDPattern.MatchString(uuid) }

// Merge applies one fetch's events onto trips, following the calendar feed
// rules of docs/research.md and docs/plan.md. It mutates trips in place and
// returns a warning for each plan event whose trip is absent from events.
func Merge(trips map[string]*Trip, events []ics.Event, now time.Time) []string {
	tripEvents := make(map[string]ics.Event)   // trip uuid -> trip event
	tripIDToUUID := make(map[string]string)    // numeric trip id -> trip uuid
	planEvents := make(map[string][]ics.Event) // trip uuid -> plan events

	var plans []ics.Event
	var warnings []string
	for _, e := range events {
		uid := e.UID()
		switch {
		case strings.HasPrefix(uid, "item-"):
			plans = append(plans, e)
		case strings.HasSuffix(uid, "@tripit.com"):
			tripUUID := strings.TrimSuffix(uid, "@tripit.com")
			if !tripUUIDPattern.MatchString(tripUUID) {
				warnings = append(warnings, fmt.Sprintf("trip event %q: the UUID is not safe as a file name, so the run skips it", uid))
				continue
			}
			tripEvents[tripUUID] = e
			if id := tripIDOf(e); id != "" {
				tripIDToUUID[id] = tripUUID
			}
		}
	}

	for _, e := range plans {
		id := tripIDOf(e)
		if id == "" {
			continue
		}
		tripUUID, ok := tripIDToUUID[id]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("plan %s: no trip event for trip id %s in this fetch", e.UID(), id))
			continue
		}
		planEvents[tripUUID] = append(planEvents[tripUUID], e)
	}

	for tripUUID, tripEvent := range tripEvents {
		fresh := append([]ics.Event{tripEvent}, planEvents[tripUUID]...)

		trip, existed := trips[tripUUID]
		if !existed {
			trip = &Trip{Schema: 1, UUID: tripUUID}
			trips[tripUUID] = trip
		}

		trip.TripID = tripIDOf(tripEvent)
		trip.Start = dateValue(tripEvent.DTStart())
		trip.End = dateValue(dtEnd(tripEvent))
		trip.InFeed = true
		trip.Events = mergeEvents(trip.Events, fresh)
	}

	for tripUUID, trip := range trips {
		if _, inFetch := tripEvents[tripUUID]; inFetch {
			continue
		}
		if !trip.InFeed {
			continue
		}
		if shouldDeleteAbsentTrip(trip, now) {
			delete(trips, tripUUID)
		}
	}

	sort.Slice(warnings, func(i, j int) bool { return warnings[i] < warnings[j] })
	return warnings
}

// mergeEvents combines the archived events with the fresh events of the
// current fetch: an event that is only different in DTSTAMP keeps its
// archived bytes, a new or changed event is replaced, and an archived event
// absent from fresh is deleted. The result is sorted by DTSTART, then UID.
func mergeEvents(archived []Event, fresh []ics.Event) []Event {
	archivedByUID := make(map[string]Event, len(archived))
	for _, e := range archived {
		archivedByUID[e.UID] = e
	}

	merged := make([]Event, 0, len(fresh))
	for _, e := range fresh {
		uid := e.UID()

		if old, ok := archivedByUID[uid]; ok {
			if oldParsed, err := ics.ParseEvents([]byte(old.ICS)); err == nil && len(oldParsed) == 1 && oldParsed[0].Equal(e) {
				merged = append(merged, old)
				continue
			}
		}

		raw, err := ics.EventBytes(e)
		if err != nil {
			continue
		}
		merged = append(merged, Event{UID: uid, ICS: string(raw)})
	}

	sortEvents(merged)
	return merged
}

// sortEvents sorts events by DTSTART, then UID. It parses each event once,
// before the sort, and not in the comparator.
func sortEvents(events []Event) {
	type keyed struct {
		start string
		event Event
	}
	keys := make([]keyed, len(events))
	for i, e := range events {
		keys[i] = keyed{start: dtStartOf(e), event: e}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].start != keys[j].start {
			return keys[i].start < keys[j].start
		}
		return keys[i].event.UID < keys[j].event.UID
	})
	for i, k := range keys {
		events[i] = k.event
	}
}

func dtStartOf(e Event) string {
	parsed, err := ics.ParseEvents([]byte(e.ICS))
	if err != nil || len(parsed) != 1 {
		return ""
	}
	return dateValue(parsed[0].DTStart())
}

func descriptionOf(e ics.Event) string {
	p, _ := e.Get("DESCRIPTION")
	return p.Raw
}

func tripIDOf(e ics.Event) string {
	m := tripIDPattern.FindStringSubmatch(descriptionOf(e))
	if m == nil {
		return ""
	}
	return m[1]
}

func dtEnd(e ics.Event) string {
	p, _ := e.Get("DTEND")
	return p.Raw
}

// dateValue extracts the YYYY-MM-DD date from a raw DTSTART or DTEND
// property whose value is an 8-digit date, for example
// "DTSTART;VALUE=DATE:20260615" -> "2026-06-15". It returns "" when raw does
// not hold an 8-digit date.
func dateValue(raw string) string {
	p := ics.Property{Raw: raw}
	v := p.Value()
	if len(v) < 8 {
		return ""
	}
	v = v[:8]
	for _, c := range v {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return v[0:4] + "-" + v[4:6] + "-" + v[6:8]
}

// shouldDeleteAbsentTrip applies the window margin rule: a trip that is
// absent from the feed and ended fewer than 83 days ago was deleted by
// TripIt, so the archive deletes it too. A trip that ended 83 or more days
// ago stays, because the exact edge of the 90-day window is not certain,
// and a kept trip is the error that a person can correct.
func shouldDeleteAbsentTrip(trip *Trip, now time.Time) bool {
	end, err := time.Parse("2006-01-02", trip.End)
	if err != nil {
		return false
	}
	daysSinceEnd := now.Sub(end)
	return daysSinceEnd < windowDays-windowMargin
}

// Write writes every trip file, the per-trip ICS files, and the combined
// tripit.ics, and it removes the files of a trip that trips no longer
// holds. It writes a file only when its content changed, using a temporary
// file and os.Rename so a crash never leaves a partial file.
func Write(dir string, trips map[string]*Trip) error {
	tripsDir := filepath.Join(dir, "trips")
	if err := os.MkdirAll(tripsDir, 0o755); err != nil {
		return fmt.Errorf("archive: make %s: %w", tripsDir, err)
	}

	kept := make(map[string]bool, len(trips))
	ordered := orderedTrips(trips)

	var all []ics.Event
	for _, trip := range ordered {
		kept[trip.UUID+".json"] = true
		kept[trip.UUID+".ics"] = true

		data, err := json.MarshalIndent(trip, "", "  ")
		if err != nil {
			return fmt.Errorf("archive: encode trip %s: %w", trip.UUID, err)
		}
		data = append(data, '\n')
		if err := writeIfChanged(tripsDir, trip.UUID+".json", data); err != nil {
			return err
		}

		tripEvents, err := tripICSEvents(trip)
		if err != nil {
			return err
		}
		all = append(all, tripEvents...)

		var buf strings.Builder
		if err := ics.WriteCalendar(&buf, tripEvents); err != nil {
			return fmt.Errorf("archive: encode calendar for trip %s: %w", trip.UUID, err)
		}
		if err := writeIfChanged(tripsDir, trip.UUID+".ics", []byte(buf.String())); err != nil {
			return err
		}
	}

	if err := removeStale(tripsDir, kept); err != nil {
		return err
	}

	var buf strings.Builder
	if err := ics.WriteCalendar(&buf, all); err != nil {
		return fmt.Errorf("archive: encode tripit.ics: %w", err)
	}
	return writeIfChanged(dir, "tripit.ics", []byte(buf.String()))
}

// orderedTrips returns trips in order of start date, then UUID, so the
// archive always makes the same bytes for the same content.
func orderedTrips(trips map[string]*Trip) []*Trip {
	ordered := make([]*Trip, 0, len(trips))
	for _, trip := range trips {
		ordered = append(ordered, trip)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start != ordered[j].Start {
			return ordered[i].Start < ordered[j].Start
		}
		return ordered[i].UUID < ordered[j].UUID
	})
	return ordered
}

// tripICSEvents parses each stored event of trip back into an ics.Event and
// sorts them by DTSTART, then UID.
func tripICSEvents(trip *Trip) ([]ics.Event, error) {
	out := make([]ics.Event, 0, len(trip.Events))
	for _, e := range trip.Events {
		parsed, err := ics.ParseEvents([]byte(e.ICS))
		if err != nil || len(parsed) != 1 {
			return nil, fmt.Errorf("archive: parse stored event %s of trip %s", e.UID, trip.UUID)
		}
		out = append(out, parsed[0])
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := dateValue(out[i].DTStart()), dateValue(out[j].DTStart())
		if si != sj {
			return si < sj
		}
		return out[i].UID() < out[j].UID()
	})
	return out, nil
}

func removeStale(tripsDir string, kept map[string]bool) error {
	entries, err := os.ReadDir(tripsDir)
	if err != nil {
		return fmt.Errorf("archive: read %s: %w", tripsDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || kept[entry.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(tripsDir, entry.Name())); err != nil {
			return fmt.Errorf("archive: remove %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// writeIfChanged writes data to dir/name only when its SHA-256 differs from
// the file already on disk.
func writeIfChanged(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	if existing, err := os.ReadFile(path); err == nil && sha256.Sum256(existing) == sha256.Sum256(data) {
		return nil
	}
	return atomicWrite(dir, name, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// atomicWrite writes the content that render produces to a temporary file
// in dir, then renames it onto dir/name. If render fails, the temporary
// file is removed and dir/name is left exactly as it was.
func atomicWrite(dir, name string, render func(w io.Writer) error) error {
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("archive: create temp file for %s: %w", name, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := render(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("archive: write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("archive: close %s: %w", name, err)
	}
	return os.Rename(tmpPath, filepath.Join(dir, name))
}
