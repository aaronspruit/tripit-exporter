package airtrail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/airtrailtest"
)

// syncFixture starts a fake AirTrail, and returns a client for it and an
// empty archive folder.
func syncFixture(t *testing.T) (*airtrailtest.Server, *Client, string) {
	t.Helper()
	server := airtrailtest.New()
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: airtrailtest.APIKey, HTTP: server.Client()}
	return server, client, t.TempDir()
}

// flight makes one wanted flight of a segment.
func flight(from, to, departure string) Flight {
	clock := departure[11:16]
	return Flight{
		From:          from,
		To:            to,
		DatePrecision: "day",
		Departure:     departure,
		DepartureTime: &clock,
		Passengers:    []Passenger{{UserID: PlaceholderUserID}},
	}
}

// readState reads the state file that a sync wrote.
func readState(t *testing.T, dir string) State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		t.Fatalf("read the state file: %v", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("read the state file: %v", err)
	}
	return state
}

func TestSyncAddsThenLeavesAnUnchangedFlightAlone(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}

	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{Delete: true})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("first run: err %v, warnings %v", err, warnings)
	}
	if result.Added != 1 || result.Updated != 0 || result.Unchanged != 0 {
		t.Errorf("first run: %+v, want one added", result)
	}
	state := readState(t, dir)
	if state.Flights["seg-1"].ID != 1 {
		t.Errorf("state holds id %d, want 1", state.Flights["seg-1"].ID)
	}

	// The second run sends nothing, because TripIt did not change.
	result, _, err = Sync(context.Background(), client, dir, wanted, Options{Delete: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Unchanged != 1 || result.Added != 0 || result.Updated != 0 {
		t.Errorf("second run: %+v, want one unchanged", result)
	}
	if saves, _ := server.Counts(); saves != 1 {
		t.Errorf("got %d saves, want 1: the second run must send nothing", saves)
	}
}

// TestSyncKeepsAnEditMadeInAirTrail proves the point of the hash: the sync
// compares what TripIt gives, so a change made in AirTrail is never undone
// while TripIt holds the same flight.
func TestSyncKeepsAnEditMadeInAirTrail(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}

	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	saved := server.Flights()[0]
	saved["note"] = "written by a person in AirTrail"

	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if got := server.Flights()[0]["note"]; got != "written by a person in AirTrail" {
		t.Errorf("the note is %v, want the edit kept", got)
	}
}

func TestSyncUpdatesAChangedFlightInPlace(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// TripIt moved the flight, so the hash changes and the sync replaces the
	// flight of the stored id instead of adding a second one.
	wanted["seg-1"] = flight("SEA", "PDX", "2026-01-02T11:30:00-08:00")
	result, _, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Updated != 1 || result.Added != 0 {
		t.Errorf("%+v, want one updated", result)
	}
	if flights := server.Flights(); len(flights) != 1 {
		t.Fatalf("got %d flights, want 1", len(flights))
	} else if flights[0]["departure"] != "2026-01-02T11:30:00-08:00" {
		t.Errorf("departure = %v, want the new time", flights[0]["departure"])
	}
	if readState(t, dir).Flights["seg-1"].ID != 1 {
		t.Errorf("the flight id changed, want the same id")
	}
}

func TestSyncDeletesAFlightThatLeftTripIt(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{
		"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00"),
		"seg-2": flight("PDX", "SEA", "2026-01-09T17:00:00-08:00"),
	}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{Delete: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// The traveler cancelled the return leg, so TripIt no longer holds it.
	delete(wanted, "seg-2")
	result, _, err := Sync(context.Background(), client, dir, wanted, Options{Delete: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Deleted != 1 {
		t.Errorf("%+v, want one deleted", result)
	}
	if flights := server.Flights(); len(flights) != 1 {
		t.Errorf("got %d flights, want 1", len(flights))
	}
	if _, still := readState(t, dir).Flights["seg-2"]; still {
		t.Errorf("the state still holds the deleted segment")
	}
}

func TestSyncKeepsTheFlightWhenDeleteIsOff(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{Delete: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	result, _, err := Sync(context.Background(), client, dir, map[string]Flight{}, Options{Delete: false})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("%+v, want nothing deleted", result)
	}
	if _, deletes := server.Counts(); deletes != 0 {
		t.Errorf("got %d deletes, want none", deletes)
	}
	if len(server.Flights()) != 1 {
		t.Errorf("the flight is gone, want it kept")
	}
}

// TestSyncDropsTheStateOfAFlightRemovedInAirTrail covers a person who
// deleted the flight in AirTrail: the delete answers "Flight not found", and
// the sync treats that as done.
func TestSyncDropsTheStateOfAFlightRemovedInAirTrail(t *testing.T) {
	_, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{Delete: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// The flight is gone from AirTrail, and from TripIt.
	if err := client.Delete(context.Background(), 1); err != nil {
		t.Fatalf("remove the flight: %v", err)
	}

	result, warnings, err := Sync(context.Background(), client, dir, map[string]Flight{}, Options{Delete: true})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err %v, warnings %v", err, warnings)
	}
	if result.Deleted != 1 || result.Failed != 0 {
		t.Errorf("%+v, want one deleted and none failed", result)
	}
	if len(readState(t, dir).Flights) != 0 {
		t.Errorf("the state still holds the flight")
	}
}

// TestSyncAddsAgainWhenTheStoredFlightIsGone covers a person who deleted the
// flight in AirTrail while TripIt still holds it.
func TestSyncAddsAgainWhenTheStoredFlightIsGone(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := client.Delete(context.Background(), 1); err != nil {
		t.Fatalf("remove the flight: %v", err)
	}

	wanted["seg-1"] = flight("SEA", "PDX", "2026-01-02T11:30:00-08:00")
	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err %v, warnings %v", err, warnings)
	}
	if result.Updated != 1 {
		t.Errorf("%+v, want one updated", result)
	}
	if len(server.Flights()) != 1 {
		t.Errorf("got %d flights, want the flight back", len(server.Flights()))
	}
	if id := readState(t, dir).Flights["seg-1"].ID; id != 2 {
		t.Errorf("state holds id %d, want the new id 2", id)
	}
}

// TestSyncKeepsGoingAfterOneRefusedFlight proves that a flight AirTrail
// refuses does not stop the run and leaves no state, so the next run tries
// it again.
func TestSyncKeepsGoingAfterOneRefusedFlight(t *testing.T) {
	server, client, dir := syncFixture(t)
	server.FailFlight("XXX", http.StatusBadRequest)
	wanted := map[string]Flight{
		"seg-bad": flight("XXX", "PDX", "2026-01-02T08:00:00-08:00"),
		"seg-ok":  flight("SEA", "PDX", "2026-01-03T08:00:00-08:00"),
	}

	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("the run stopped: %v", err)
	}
	if result.Added != 1 || result.Failed != 1 {
		t.Errorf("%+v, want one added and one failed", result)
	}
	if len(warnings) != 1 {
		t.Errorf("got warnings %v, want one", warnings)
	}
	if _, kept := readState(t, dir).Flights["seg-bad"]; kept {
		t.Errorf("the refused flight got a state entry, want none")
	}
}

// TestSyncStopsOnARejectedKey proves that a bad API key stops the run, and
// keeps the state of the work already done.
func TestSyncStopsOnARejectedKey(t *testing.T) {
	server, client, dir := syncFixture(t)
	server.RejectKey()
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}

	_, _, err := Sync(context.Background(), client, dir, wanted, Options{})

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want an *AuthError", err)
	}
}

func TestLoadStateOfAFolderWithNoFile(t *testing.T) {
	state, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(state.Flights) != 0 {
		t.Errorf("got %d flights, want none", len(state.Flights))
	}
}

func TestLoadStateOfABrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	if _, err := LoadState(dir); err == nil {
		t.Errorf("got no error, want one for a broken state file")
	}
}

// TestSyncDoesNotDuplicateOnAFailedUpdate proves that a failure other than a
// missing flight keeps the stored id. A retry with no id would add a second
// copy of a flight that AirTrail still holds.
func TestSyncDoesNotDuplicateOnAFailedUpdate(t *testing.T) {
	server, client, dir := syncFixture(t)
	wanted := map[string]Flight{"seg-1": flight("SEA", "PDX", "2026-01-02T08:00:00-08:00")}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// TripIt moved the flight, and AirTrail answers the update with a 500.
	server.FailFlight("SEA", http.StatusInternalServerError)
	wanted["seg-1"] = flight("SEA", "PDX", "2026-01-02T11:30:00-08:00")
	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("the run stopped: %v", err)
	}
	if result.Failed != 1 || result.Added != 0 || result.Updated != 0 {
		t.Errorf("%+v, want one failed and nothing written", result)
	}
	if len(warnings) != 1 {
		t.Errorf("got warnings %v, want one", warnings)
	}
	if saves, _ := server.Counts(); saves != 2 {
		t.Errorf("the run sent %d saves, want 2: the add and the one failed update", saves)
	}
	if flights := server.Flights(); len(flights) != 1 {
		t.Errorf("got %d flights, want 1", len(flights))
	}
	// The state keeps the old id and the old hash, so the next run tries the
	// same update again.
	if got := readState(t, dir).Flights["seg-1"]; got.ID != 1 || got.Hash != flight("SEA", "PDX", "2026-01-02T08:00:00-08:00").Hash() {
		t.Errorf("state holds %+v, want the entry of the first run", got)
	}
}

// withCodes adds an airline and an aircraft to a wanted flight.
func withCodes(f Flight, airline, aircraft string) Flight {
	f.Airline = &airline
	f.Aircraft = &aircraft
	return f
}

// TestSyncSendsTheFlightAgainWithoutARefusedCode covers a code that this
// exporter holds and the AirTrail instance does not. AirTrail refuses the
// whole flight, so the sync drops the field and keeps the flight.
func TestSyncSendsTheFlightAgainWithoutARefusedCode(t *testing.T) {
	server, client, dir := syncFixture(t)
	server.Hold("aircraft", "B738")
	wanted := map[string]Flight{
		"seg-1": withCodes(flight("SEA", "PDX", "2026-01-02T08:00:00-08:00"), "ASA", "B39M"),
	}

	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("the run stopped: %v", err)
	}
	if result.Added != 1 || result.Failed != 0 {
		t.Fatalf("%+v, want one added and none failed", result)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "aircraft B39M") {
		t.Errorf("got warnings %v, want one naming aircraft B39M", warnings)
	}

	flights := server.Flights()
	if len(flights) != 1 {
		t.Fatalf("got %d flights, want 1", len(flights))
	}
	if flights[0]["aircraft"] != nil {
		t.Errorf("aircraft = %v, want it dropped", flights[0]["aircraft"])
	}
	if flights[0]["airline"] != "ASA" {
		t.Errorf("airline = %v, want ASA kept", flights[0]["airline"])
	}
}

// TestSyncDropsBothRefusedCodes covers a flight whose airline and aircraft
// are both absent from the instance.
func TestSyncDropsBothRefusedCodes(t *testing.T) {
	server, client, dir := syncFixture(t)
	server.Hold("airline", "UAL")
	server.Hold("aircraft", "B738")
	wanted := map[string]Flight{
		"seg-1": withCodes(flight("SEA", "PDX", "2026-01-02T08:00:00-08:00"), "ASA", "B39M"),
	}

	result, warnings, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("the run stopped: %v", err)
	}
	if result.Added != 1 {
		t.Fatalf("%+v, want one added", result)
	}
	if len(warnings) != 2 {
		t.Errorf("got warnings %v, want two", warnings)
	}
	flights := server.Flights()
	if flights[0]["airline"] != nil || flights[0]["aircraft"] != nil {
		t.Errorf("flight = %v, want no airline and no aircraft", flights[0])
	}
}

// TestSyncHashStaysWholeAfterADroppedCode proves that the state holds the
// hash of what TripIt gives, and not of the body that AirTrail accepted. The
// next run therefore sends nothing, instead of trying the refused code again
// on every run.
func TestSyncHashStaysWholeAfterADroppedCode(t *testing.T) {
	server, client, dir := syncFixture(t)
	server.Hold("aircraft", "B738")
	wanted := map[string]Flight{
		"seg-1": withCodes(flight("SEA", "PDX", "2026-01-02T08:00:00-08:00"), "ASA", "B39M"),
	}
	if _, _, err := Sync(context.Background(), client, dir, wanted, Options{}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	result, _, err := Sync(context.Background(), client, dir, wanted, Options{})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Unchanged != 1 {
		t.Errorf("%+v, want one unchanged", result)
	}
	if saves, _ := server.Counts(); saves != 2 {
		t.Errorf("got %d saves, want 2: one refused and one accepted, then nothing", saves)
	}
}

func TestRefusedField(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&FlightError{Status: 500, Message: "Invalid airline"}, "airline"},
		{&FlightError{Status: 500, Message: "Invalid aircraft"}, "aircraft"},
		{&FlightError{Status: 400, Message: "Arrival must be after departure"}, ""},
		{&NotFoundError{ID: 1}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := RefusedField(c.err); got != c.want {
			t.Errorf("RefusedField(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
