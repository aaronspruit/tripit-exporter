package airtrail

import (
	"encoding/json"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
	"github.com/aaronspruit/tripit-exporter/internal/testutil"
)

// tripWithV2 makes an archived trip whose v2 object holds detail.
func tripWithV2(uuid, detail string) *archive.Trip {
	return &archive.Trip{Schema: 1, UUID: uuid, InFeed: true, V2: json.RawMessage(detail)}
}

// twoSegments is a trip with a connection: one reservation, two legs.
const twoSegments = `{
  "AirObject": [{
    "is_client_traveler": "true",
    "Segment": [
      {
        "uuid": "seg-1",
        "start_airport_code": "MEL", "end_airport_code": "SYD",
        "start_terminal": "2", "start_gate": "16",
        "end_terminal": "1", "end_gate": "61",
        "StartDateTime": {"date": "2014-08-16", "time": "11:20:00", "utc_offset": "+10:00"},
        "EndDateTime": {"date": "2014-08-16", "time": "12:45:00", "utc_offset": "+10:00"},
        "marketing_airline": "United Airlines", "marketing_airline_code": "UA",
        "marketing_flight_number": "840",
        "aircraft": "777", "aircraft_display_name": "Boeing 777",
        "seats": "23G", "service_class": "Coach Class - M"
      },
      {
        "uuid": "seg-2",
        "start_airport_code": "SYD", "end_airport_code": "SFO",
        "StartDateTime": {"date": "2014-08-16", "time": "14:45:00", "utc_offset": "+10:00"},
        "EndDateTime": {"date": "2014-08-16", "time": "11:25:00", "utc_offset": "-07:00"},
        "marketing_airline": "United Airlines", "marketing_airline_code": "UA",
        "marketing_flight_number": "870",
        "aircraft": "73H", "aircraft_display_name": "Boeing 737-800 (winglets) Passenger/BBJ2",
        "seats": "29H, 29J", "service_class": "Business Class"
      }
    ]
  }]
}`

func TestBuildSegments(t *testing.T) {
	trips := map[string]*archive.Trip{"trip-1": tripWithV2("trip-1", twoSegments)}

	flights, warnings := Build(trips, "", DefaultCodes())

	if len(flights) != 2 {
		t.Fatalf("got %d flights, want 2", len(flights))
	}
	// The generic TripIt code "777" names every Boeing 777, so the first leg
	// keeps no aircraft and makes a warning.
	if len(warnings) != 1 {
		t.Fatalf("got warnings %v, want one about the aircraft", warnings)
	}

	got, err := json.MarshalIndent(flights, "", "  ")
	if err != nil {
		t.Fatalf("encode the flights: %v", err)
	}
	testutil.Golden(t, "build_segments", append(got, '\n'))
}

func TestBuildFieldsOfOneSegment(t *testing.T) {
	trips := map[string]*archive.Trip{"trip-1": tripWithV2("trip-1", twoSegments)}
	flights, _ := Build(trips, "aaron", DefaultCodes())

	first, ok := flights["seg-1"]
	if !ok {
		t.Fatalf("got no flight for seg-1")
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"from", first.From, "MEL"},
		{"to", first.To, "SYD"},
		{"departure", first.Departure, "2014-08-16T11:20:00+10:00"},
		{"departureTime", deref(first.DepartureTime), "11:20"},
		{"arrival", deref(first.Arrival), "2014-08-16T12:45:00+10:00"},
		{"arrivalTime", deref(first.ArrivalTime), "12:45"},
		{"flightNumber", deref(first.FlightNumber), "UA840"},
		{"airline", deref(first.Airline), "UAL"},
		{"departureGate", deref(first.DepartureGate), "16"},
		{"arrivalTerminal", deref(first.ArrivalTerminal), "1"},
		{"userId", first.Passengers[0].UserID, "aaron"},
		{"seatNumber", deref(first.Passengers[0].SeatNumber), "23G"},
		{"seatClass", deref(first.Passengers[0].SeatClass), "economy"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	second := flights["seg-2"]
	if got := deref(second.Aircraft); got != "B738" {
		t.Errorf("aircraft of seg-2 = %q, want B738", got)
	}
	// TripIt holds every seat of the booking in one field.
	if got := deref(second.Passengers[0].SeatNumber); got != "29H" {
		t.Errorf("seatNumber of seg-2 = %q, want 29H", got)
	}
	if got := deref(second.Passengers[0].SeatClass); got != "business" {
		t.Errorf("seatClass of seg-2 = %q, want business", got)
	}
}

func TestBuildDefaultsToThePlaceholderUser(t *testing.T) {
	trips := map[string]*archive.Trip{"trip-1": tripWithV2("trip-1", twoSegments)}
	flights, _ := Build(trips, "", DefaultCodes())

	if got := flights["seg-1"].Passengers[0].UserID; got != PlaceholderUserID {
		t.Errorf("userId = %q, want %q", got, PlaceholderUserID)
	}
}

// TestBuildDropsAnArrivalBeforeItsDeparture covers the one TripIt data error
// in the operator archive: AirTrail answers 400 for an arrival that is not
// after its departure, so the flight keeps its departure and loses the
// arrival.
func TestBuildDropsAnArrivalBeforeItsDeparture(t *testing.T) {
	const backwards = `{"AirObject": {"is_client_traveler": "true", "Segment": {
      "uuid": "seg-bad",
      "start_airport_code": "IAH", "end_airport_code": "PVR",
      "StartDateTime": {"date": "2011-04-17", "time": "11:35:00", "utc_offset": "-05:00"},
      "EndDateTime": {"date": "2011-04-17", "time": "02:10:00", "utc_offset": "-05:00"},
      "marketing_airline": "United Airlines", "marketing_airline_code": "UA",
      "marketing_flight_number": "1606"
    }}}`

	flights, warnings := Build(map[string]*archive.Trip{"t": tripWithV2("t", backwards)}, "", DefaultCodes())

	flight, ok := flights["seg-bad"]
	if !ok {
		t.Fatalf("got no flight, want the flight with no arrival")
	}
	if flight.Arrival != nil || flight.ArrivalTime != nil {
		t.Errorf("arrival = %v/%v, want both nil", deref(flight.Arrival), deref(flight.ArrivalTime))
	}
	if flight.Departure != "2011-04-17T11:35:00-05:00" {
		t.Errorf("departure = %q, want the TripIt departure", flight.Departure)
	}
	if len(warnings) != 1 {
		t.Errorf("got warnings %v, want one about the arrival", warnings)
	}
}

// TestBuildAcceptsOneObjectOrAnArray covers the TripIt shape: one object
// when the trip holds one reservation, an array when it holds more.
func TestBuildAcceptsOneObjectOrAnArray(t *testing.T) {
	const single = `{"AirObject": {"is_client_traveler": "true", "Segment": {
      "uuid": "seg-solo",
      "start_airport_code": "SEA", "end_airport_code": "PDX",
      "StartDateTime": {"date": "2026-01-02", "time": "08:00:00", "utc_offset": "-08:00"},
      "EndDateTime": {"date": "2026-01-02", "time": "08:55:00", "utc_offset": "-08:00"},
      "marketing_airline": "Alaska Airlines", "marketing_airline_code": "AS",
      "marketing_flight_number": "2040"
    }}}`

	flights, warnings := Build(map[string]*archive.Trip{"t": tripWithV2("t", single)}, "", DefaultCodes())

	if len(flights) != 1 {
		t.Fatalf("got %d flights, want 1", len(flights))
	}
	if len(warnings) != 0 {
		t.Errorf("got warnings %v, want none", warnings)
	}
	if got := deref(flights["seg-solo"].Airline); got != "ASA" {
		t.Errorf("airline = %q, want ASA", got)
	}
}

// TestBuildSkipsWhatDoesNotBelong covers the three segments that never reach
// AirTrail: a hidden segment, a segment with no airport code, and a trip
// that another traveler shares.
func TestBuildSkipsWhatDoesNotBelong(t *testing.T) {
	const mixed = `{"AirObject": [
      {"is_client_traveler": "false", "Segment": {"uuid": "shared",
        "start_airport_code": "LHR", "end_airport_code": "DUB",
        "StartDateTime": {"date": "2026-02-01", "time": "09:00:00", "utc_offset": "+00:00"}}},
      {"is_client_traveler": "true", "Segment": [
        {"uuid": "hidden", "is_hidden": "true",
         "start_airport_code": "LHR", "end_airport_code": "DUB",
         "StartDateTime": {"date": "2026-02-01", "time": "09:00:00", "utc_offset": "+00:00"}},
        {"uuid": "no-airport",
         "StartDateTime": {"date": "2026-02-01", "time": "09:00:00", "utc_offset": "+00:00"}},
        {"uuid": "no-date", "start_airport_code": "LHR", "end_airport_code": "DUB"}
      ]}
    ]}`

	flights, warnings := Build(map[string]*archive.Trip{"t": tripWithV2("t", mixed)}, "", DefaultCodes())

	if len(flights) != 0 {
		t.Fatalf("got %d flights, want none", len(flights))
	}
	// The hidden segment and the shared trip are normal, so they make no
	// warning. The two broken segments each make one.
	if len(warnings) != 2 {
		t.Errorf("got warnings %v, want two", warnings)
	}
}

func TestBuildSkipsATripWithNoV2Object(t *testing.T) {
	trips := map[string]*archive.Trip{
		"empty": {Schema: 1, UUID: "empty", InFeed: true},
		"null":  tripWithV2("null", "null"),
	}

	flights, warnings := Build(trips, "", DefaultCodes())

	if len(flights) != 0 || len(warnings) != 0 {
		t.Errorf("got %d flights and warnings %v, want none of either", len(flights), warnings)
	}
}

// TestHashIgnoresTheID proves that the stored id never changes the hash, so
// an update does not look like a change on the next run.
func TestHashIgnoresTheID(t *testing.T) {
	flight := Flight{From: "SEA", To: "PDX", Departure: "2026-01-02T08:00:00-08:00"}
	withID := flight
	withID.ID = 42

	if flight.Hash() != withID.Hash() {
		t.Errorf("the hash changed with the id")
	}
	changed := flight
	changed.To = "SFO"
	if flight.Hash() == changed.Hash() {
		t.Errorf("the hash did not change with the destination")
	}
}

func TestSeatClass(t *testing.T) {
	cases := map[string]string{
		"Coach Class - M":            "economy",
		"Economy":                    "economy",
		"Premium Economy":            "economy+",
		"Business Class":             "business",
		"First Class":                "first",
		"":                           "",
		"Unknown Cabin Of Some Kind": "",
	}
	for input, want := range cases {
		if got := seatClass(input); got != want {
			t.Errorf("seatClass(%q) = %q, want %q", input, got, want)
		}
	}
}

// deref reads an optional string field for a comparison.
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
