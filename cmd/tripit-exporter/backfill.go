package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/ics"
	"github.com/aaronspruit/tripit-exporter/internal/tripitweb"
)

// runBackfill reads a session cookie from stdin, then fills the archive
// under outputDir(env) with every trip of the account: the v2 objects of
// each trip, and the events of a trip that the feed run has not reached
// yet. See docs/plan.md for the exit code table.
func runBackfill(env map[string]string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	outputDir := outputDir(env)

	cookie, err := readCookie(stdin, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}

	client := newWebClient(env, cookie, stdout)
	ctx := context.Background()

	// Each step writes a progress line to stdout before its first request,
	// so an operator can see which request a slow run waits on.
	_, _ = fmt.Fprintln(stdout, "tripit-exporter: checking the cookie")
	if err := client.Profile(ctx); err != nil {
		return backfillExitCode(err, stderr)
	}

	return syncTrips(ctx, client, outputDir, "backfill", needsBackfill, stdout, stderr, now)
}

// newWebClient returns a web API v2 client that sends cookie and writes its
// lines to stdout. TRIPIT_WEB_BASE_URL is empty in production, so the client
// calls the real TripIt host. A test sets it to a fake server's URL.
func newWebClient(env map[string]string, cookie string, stdout io.Writer) *tripitweb.Client {
	return &tripitweb.Client{
		Cookie:  cookie,
		BaseURL: env["TRIPIT_WEB_BASE_URL"],
		Sleep:   backfillSleep,
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(stdout, "tripit-exporter: "+format+"\n", args...)
		},
		Verbose:   boolEnv(env["TRIPIT_VERBOSE"]),
		UserAgent: env["TRIPIT_USER_AGENT"],
	}
}

// needsBackfill reports whether the backfill reads a trip: a trip with no
// file, no v2 object, or no events and no empty download. A second backfill
// run therefore skips each trip that the first run finished.
func needsBackfill(trip *archive.Trip) bool {
	return trip == nil || !hasV2(trip) || (len(trip.Events) == 0 && !trip.EmptyDownload)
}

// syncTrips lists every trip of the account, and reads each trip that want
// selects. want gets nil for a trip that the archive does not hold. verb
// names the work in the progress lines. It writes the archive after each
// trip, and returns the exit code.
func syncTrips(ctx context.Context, client *tripitweb.Client, outputDir, verb string, want func(*archive.Trip) bool, stdout, stderr io.Writer, now time.Time) int {
	_, _ = fmt.Fprintln(stdout, "tripit-exporter: listing the trips")
	trips, err := listAllTrips(ctx, client)
	if err != nil {
		return backfillExitCode(err, stderr)
	}

	archived, err := archive.Load(outputDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}

	listed := make(map[string]bool, len(trips))
	var todo []string
	for _, raw := range trips {
		uuid := tripitweb.UUIDField(raw)
		if uuid == "" {
			continue
		}
		listed[uuid] = true
		if !archive.ValidTripUUID(uuid) {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: warning: trip %q: the UUID is not safe as a file name, so the %s skips it\n", uuid, verb)
			continue
		}
		if !want(archived[uuid]) {
			continue
		}
		todo = append(todo, uuid)
	}
	deleted := pruneDeleted(archived, listed, now)
	_, _ = fmt.Fprintf(stdout, "tripit-exporter: %d trips, %d to %s, %d deleted at TripIt\n", len(trips), len(todo), verb, len(deleted))
	for _, uuid := range deleted {
		_, _ = fmt.Fprintf(stdout, "tripit-exporter: trip %s is gone from TripIt, so the archive drops it\n", uuid)
	}
	if len(deleted) > 0 {
		if err := archive.Write(outputDir, archived); err != nil {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
			return 2
		}
	}

	for i, uuid := range todo {
		_, _ = fmt.Fprintf(stdout, "tripit-exporter: trip %d of %d: %s\n", i+1, len(todo), uuid)

		warning, err := backfillTrip(ctx, client, archived, uuid)
		if err != nil {
			return backfillExitCode(err, stderr)
		}
		if warning != nil {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: warning: %v; the trip file keeps its v2 object with no events, and the next backfill tries the download again\n", warning)
		}
		if err := archive.Write(outputDir, archived); err != nil {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
			return 2
		}
	}
	_, _ = fmt.Fprintln(stdout, "tripit-exporter: done")
	return 0
}

// pruneDeleted removes from archived each trip that a person deleted at
// TripIt, and returns the UUID of each one. listed holds the UUID of every
// trip that the account still has.
//
// A trip is deleted only when it ended before now. The trip list asks for
// every past trip of every traveler, so it holds each past trip that still
// exists; the upcoming half asks for the trips of this traveler alone, so a
// trip that another traveler shares and that has not ended yet is absent
// from the list although it exists.
//
// A trip inside the feed window also stays, because Merge owns that trip
// and deletes it as soon as it leaves a fetch.
//
// An empty list deletes nothing, because a list that holds no trip is a
// failed read more probably than an empty account.
func pruneDeleted(archived map[string]*archive.Trip, listed map[string]bool, now time.Time) []string {
	if len(listed) == 0 {
		return nil
	}
	today := now.Format("2006-01-02")

	var deleted []string
	for uuid, trip := range archived {
		if listed[uuid] || trip.End == "" || trip.End >= today {
			continue
		}
		if archive.WithinFeedWindow(trip, now) {
			continue
		}
		delete(archived, uuid)
		deleted = append(deleted, uuid)
	}
	sort.Strings(deleted)
	return deleted
}

// boolEnv reports whether an environment variable such as TRIPIT_VERBOSE is
// on. It accepts the values of strconv.ParseBool, and an empty or other
// value is false.
func boolEnv(value string) bool {
	on, err := strconv.ParseBool(value)
	return err == nil && on
}

// backfillSleep replaces time.Sleep in the backfill client. It is nil in
// production; a test sets it so that no test waits for real.
var backfillSleep func(time.Duration)

// backfillTrip reads the v2 detail of uuid, and downloads its events when
// the archive holds none yet and no earlier download held zero events. It mutates archived in place. A download that
// fails for this trip alone, with a *tripitweb.DownloadError or a calendar
// that does not parse, returns a warning: the trip keeps its v2 object with
// no events, so the next run tries the download again. An error stops the
// run.
func backfillTrip(ctx context.Context, client *tripitweb.Client, archived map[string]*archive.Trip, uuid string) (warning, err error) {
	detail, err := client.GetTripDetail(ctx, uuid)
	if err != nil {
		return nil, err
	}

	trip, ok := archived[uuid]
	if !ok {
		trip = &archive.Trip{Schema: 1, UUID: uuid}
		archived[uuid] = trip
	}
	if !sameV2(trip.V2, detail) {
		trip.V2 = detail
	}
	if start, end := tripDates(detail); start != "" {
		trip.Start, trip.End = start, end
	}

	if len(trip.Events) > 0 || trip.EmptyDownload {
		return nil, nil
	}

	calendar, err := client.DownloadICS(ctx, uuid, uuid+".ics")
	var downloadErr *tripitweb.DownloadError
	if errors.As(err, &downloadErr) {
		return err, nil
	}
	if err != nil {
		return nil, err
	}
	events, err := ics.ParseEvents(calendar)
	if err != nil {
		return fmt.Errorf("parse downloaded calendar for trip %s: %w", uuid, err), nil
	}
	var parsed []archive.Event
	for _, e := range events {
		raw, err := ics.EventBytes(e)
		if err != nil {
			return fmt.Errorf("encode downloaded event for trip %s: %w", uuid, err), nil
		}
		parsed = append(parsed, archive.Event{UID: e.UID(), ICS: string(raw)})
		// The v2 object holds no numeric trip ID, so it comes from the link
		// in the trip event.
		if e.UID() == uuid+"@tripit.com" {
			trip.TripID = archive.TripIDOf(e)
		}
	}
	trip.Events = parsed
	trip.EmptyDownload = len(parsed) == 0
	return nil, nil
}

// listAllTrips lists every trip of the account: the future trips the
// traveler owns, and every past trip, including the trips that another
// traveler shares.
func listAllTrips(ctx context.Context, client *tripitweb.Client) ([]json.RawMessage, error) {
	past, err := client.ListTrips(ctx, "exclude_types=weather&past=true&traveler=all")
	if err != nil {
		return nil, err
	}
	upcoming, err := client.ListTrips(ctx, "exclude_types=weather&past=false&traveler=true")
	if err != nil {
		return nil, err
	}
	return tripitweb.DedupeByUUID(append(past, upcoming...)), nil
}

func backfillExitCode(err error, stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
	return tripitweb.ExitCode(err)
}

// hasV2 reports whether trip already holds a v2 object. A second backfill
// run skips a trip that holds a v2 object and either events or an empty
// download.
func hasV2(trip *archive.Trip) bool {
	return len(trip.V2) > 0 && string(trip.V2) != "null"
}

// sameV2 reports whether two trip detail responses hold the same data. It
// ignores the top-level timestamp, which changes with each request, and the
// layout of the JSON, because the archive writes v2 indented.
func sameV2(a, b json.RawMessage) bool {
	var va, vb map[string]any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	delete(va, "timestamp")
	delete(vb, "timestamp")
	return reflect.DeepEqual(va, vb)
}

// tripDates reads start_date and end_date from a trip detail response, as
// the TripIt API v1 documentation describes the Trip object.
func tripDates(detail json.RawMessage) (start, end string) {
	var v struct {
		Trip struct {
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		} `json:"Trip"`
	}
	if err := json.Unmarshal(detail, &v); err != nil {
		return "", ""
	}
	return v.Trip.StartDate, v.Trip.EndDate
}

// readCookie prompts for the session cookie and reads one line, of any
// length. When stdin is a terminal, it sets cookieInputMode first, so the
// cookie never appears on screen. The line ends at "\n" or "\r", because a
// terminal with no line editing can send either one for Enter. It never
// writes the cookie anywhere but into the returned string.
func readCookie(stdin io.Reader, stdout io.Writer) (string, error) {
	_, _ = fmt.Fprint(stdout, "TripIt session cookie: ")

	if f, ok := stdin.(*os.File); ok && isTerminal(f) {
		restore, err := cookieInputMode(f)
		if err == nil {
			defer restore()
			defer func() { _, _ = fmt.Fprintln(stdout) }()
		}
	}

	var line strings.Builder
	r := bufio.NewReader(stdin)
	for {
		b, err := r.ReadByte()
		if errors.Is(err, io.EOF) || b == '\n' || b == '\r' {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read cookie: %w", err)
		}
		// With canonical mode off, the terminal sends backspace as a byte.
		// A cookie never holds that byte, so it erases the byte before it.
		if b == 0x7f || b == '\b' {
			s := line.String()
			line.Reset()
			line.WriteString(s[:max(len(s)-1, 0)])
			continue
		}
		line.WriteByte(b)
	}
	cookie := strings.TrimSpace(line.String())
	if cookie == "" {
		return "", errors.New("the cookie must not be empty")
	}
	return cookie, nil
}
