package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
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
	client := &tripitweb.Client{Cookie: cookie, BaseURL: env["TRIPIT_WEB_BASE_URL"]}
	ctx := context.Background()

	if err := client.Profile(ctx); err != nil {
		return backfillExitCode(err, stderr)
	}

	trips, err := listAllTrips(ctx, client)
	if err != nil {
		return backfillExitCode(err, stderr)
	}

	archived, err := archive.Load(outputDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
		return 2
	}

	paced := false
	for _, raw := range trips {
		uuid := uuidField(raw)
		if uuid == "" {
			continue
		}
		if trip, ok := archived[uuid]; ok && hasV2(trip) {
			continue
		}

		if paced {
			client.Pace()
		}
		paced = true

		if err := backfillTrip(ctx, client, archived, uuid); err != nil {
			return backfillExitCode(err, stderr)
		}
		if err := archive.Write(outputDir, archived); err != nil {
			_, _ = fmt.Fprintf(stderr, "tripit-exporter: %v\n", err)
			return 2
		}
	}
	return 0
}

// backfillTrip reads the v2 detail of uuid, and downloads its events when
// the archive holds none yet. It mutates archived in place.
func backfillTrip(ctx context.Context, client *tripitweb.Client, archived map[string]*archive.Trip, uuid string) error {
	detail, err := client.GetTripDetail(ctx, uuid)
	if err != nil {
		return err
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
	if id := relativeURLTripID(detail); id != "" {
		trip.TripID = id
	}

	if len(trip.Events) > 0 {
		return nil
	}

	calendar, err := client.DownloadICS(ctx, uuid, uuid+".ics")
	if err != nil {
		return err
	}
	events, err := ics.ParseEvents(calendar)
	if err != nil {
		return fmt.Errorf("tripit-exporter: parse downloaded calendar for trip %s: %w", uuid, err)
	}
	for _, e := range events {
		raw, err := ics.EventBytes(e)
		if err != nil {
			return fmt.Errorf("tripit-exporter: encode downloaded event for trip %s: %w", uuid, err)
		}
		trip.Events = append(trip.Events, archive.Event{UID: e.UID(), ICS: string(raw)})
	}
	return nil
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

// hasV2 reports whether trip already holds a v2 object, so a second
// backfill run skips it.
func hasV2(trip *archive.Trip) bool {
	return len(trip.V2) > 0 && string(trip.V2) != "null"
}

func uuidField(raw json.RawMessage) string {
	var v struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.UUID
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

// relativeURLTripIDPattern matches the numeric trip ID inside a trip's
// relative_url field, for example "/trip/show/id/123456789".
var relativeURLTripIDPattern = regexp.MustCompile(`/trip/show/id/(\d+)`)

func relativeURLTripID(detail json.RawMessage) string {
	var v struct {
		Trip struct {
			RelativeURL string `json:"relative_url"`
		} `json:"Trip"`
	}
	if err := json.Unmarshal(detail, &v); err != nil {
		return ""
	}
	m := relativeURLTripIDPattern.FindStringSubmatch(v.Trip.RelativeURL)
	if m == nil {
		return ""
	}
	return m[1]
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
