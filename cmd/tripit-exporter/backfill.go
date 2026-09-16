package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/ics"
	"github.com/aaronspruit/tripit-exporter/internal/tripitweb"
)

// runBackfill reads a session cookie from stdin, then fills the archive
// under env["OUTPUT_DIR"] with every trip of the account: the v2 objects of
// each trip, and the events of a trip that the feed run has not reached
// yet. See docs/plan.md for the exit code table.
func runBackfill(env map[string]string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	outputDir := env["OUTPUT_DIR"]
	if outputDir == "" {
		_, _ = fmt.Fprintln(stderr, "tripit-exporter: OUTPUT_DIR is required")
		return 2
	}

	cookie, err := readCookie(stdin, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}

	// TRIPIT_WEB_BASE_URL is empty in production, so the client calls the
	// real TripIt host. A test sets it to a fake server's URL.
	client := &tripitweb.Client{Cookie: cookie, BaseURL: env["TRIPIT_WEB_BASE_URL"], Sleep: backfillSleep}
	ctx := context.Background()

	// Each step writes a progress line to stdout before its first request,
	// so an operator can see which request a slow run waits on.
	_, _ = fmt.Fprintln(stdout, "tripit-exporter: checking the cookie")
	if err := client.Profile(ctx); err != nil {
		return backfillExitCode(err, stderr)
	}

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

	var todo []string
	for _, raw := range trips {
		uuid := tripitweb.UUIDField(raw)
		if uuid == "" {
			continue
		}
		if !archive.ValidTripUUID(uuid) {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: warning: trip %q: the UUID is not safe as a file name, so the backfill skips it\n", uuid)
			continue
		}
		if trip, ok := archived[uuid]; ok && hasV2(trip) && (len(trip.Events) > 0 || trip.EmptyDownload) {
			continue
		}
		todo = append(todo, uuid)
	}
	_, _ = fmt.Fprintf(stdout, "tripit-exporter: %d trips, %d to backfill\n", len(trips), len(todo))

	for i, uuid := range todo {
		client.Pace()
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

// backfillSleep replaces time.Sleep in the backfill client. It is nil in
// production; a test sets it so that no test waits for real.
var backfillSleep func(time.Duration)

// backfillTrip reads the v2 detail of uuid, and downloads its events when
// the archive holds none yet. It mutates archived in place. A download that
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
	trip.V2 = detail
	if start, end := tripDates(detail); start != "" {
		trip.Start, trip.End = start, end
	}

	if len(trip.Events) > 0 {
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
	client.Pace()
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

// readCookie prompts for the session cookie and reads one line. When stdin
// is a terminal, it turns off the echo first, so the cookie never appears
// on screen. It never writes the cookie anywhere but into the returned
// string.
func readCookie(stdin io.Reader, stdout io.Writer) (string, error) {
	_, _ = fmt.Fprint(stdout, "TripIt session cookie: ")

	if f, ok := stdin.(*os.File); ok && isTerminal(f) {
		restore, err := disableEcho(f)
		if err == nil {
			defer restore()
			defer func() { _, _ = fmt.Fprintln(stdout) }()
		}
	}

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read cookie: %w", err)
	}
	cookie := strings.TrimSpace(line)
	if cookie == "" {
		return "", errors.New("the cookie must not be empty")
	}
	return cookie, nil
}
