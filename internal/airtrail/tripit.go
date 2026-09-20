package airtrail

import (
	"encoding/json"
	"strings"
	"time"
)

// airObject is one air reservation of a TripIt v2 trip detail.
type airObject struct {
	IsClientTraveler string   `json:"is_client_traveler"`
	Segment          segments `json:"Segment"`
}

// segment is one leg of an air reservation. Every TripIt v2 value is a
// string, including a boolean and a number.
type segment struct {
	UUID                  string   `json:"uuid"`
	IsHidden              string   `json:"is_hidden"`
	StartAirportCode      string   `json:"start_airport_code"`
	EndAirportCode        string   `json:"end_airport_code"`
	StartTerminal         string   `json:"start_terminal"`
	StartGate             string   `json:"start_gate"`
	EndTerminal           string   `json:"end_terminal"`
	EndGate               string   `json:"end_gate"`
	StartDateTime         dateTime `json:"StartDateTime"`
	EndDateTime           dateTime `json:"EndDateTime"`
	MarketingAirline      string   `json:"marketing_airline"`
	MarketingAirlineCode  string   `json:"marketing_airline_code"`
	MarketingFlightNumber string   `json:"marketing_flight_number"`
	Aircraft              string   `json:"aircraft"`
	AircraftDisplayName   string   `json:"aircraft_display_name"`
	Seats                 string   `json:"seats"`
	ServiceClass          string   `json:"service_class"`
}

// dateTime is one end of a segment: a local date, a local clock time, and
// the offset of the local zone at that moment.
type dateTime struct {
	Date      string `json:"date"`
	Time      string `json:"time"`
	UTCOffset string `json:"utc_offset"`
}

// parts returns the date as YYYY-MM-DD with the offset of the zone, and the
// clock time as HH:MM. AirTrail reads the date from the first and the clock
// time from the second, and merges them in the zone of its own airport
// record. Either is empty when TripIt gives too little.
func (d dateTime) parts() (date, clock string) {
	if d.Date == "" || d.Time == "" {
		return "", ""
	}
	offset := d.UTCOffset
	if offset == "" || offset == "Z" {
		offset = "+00:00"
	}
	clock = d.Time
	if len(clock) > 5 {
		clock = clock[:5]
	}
	return d.Date + "T" + d.Time + offset, clock
}

// instant returns the moment that the date, the clock time and the offset
// name together. It reports false when TripIt gives too little to place the
// moment on the line.
func (d dateTime) instant() (time.Time, bool) {
	offset := d.UTCOffset
	if offset == "" || offset == "Z" {
		offset = "+00:00"
	}
	t, err := time.Parse(time.RFC3339, d.Date+"T"+d.Time+offset)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// after reports whether d is later than other. It reports true when either
// side cannot be placed, because a comparison that cannot run must not drop
// a field.
func (d dateTime) after(other dateTime) bool {
	a, aok := d.instant()
	b, bok := other.instant()
	if !aok || !bok {
		return true
	}
	return a.After(b)
}

// airObjects holds the air reservations of a trip. TripIt writes one object
// when a trip holds one, and an array when it holds more.
type airObjects []airObject

func (a *airObjects) UnmarshalJSON(data []byte) error {
	return unmarshalOneOrMany(data, (*[]airObject)(a))
}

// segments holds the legs of a reservation, with the same one-or-many shape.
type segments []segment

func (s *segments) UnmarshalJSON(data []byte) error {
	return unmarshalOneOrMany(data, (*[]segment)(s))
}

// unmarshalOneOrMany reads a TripIt value that is one object when the trip
// holds one, and an array when it holds more.
func unmarshalOneOrMany[T any](data []byte, out *[]T) error {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if trimmed == "" || strings.HasPrefix(trimmed, "null") {
		*out = nil
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal(data, out)
	}
	var one T
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	*out = []T{one}
	return nil
}
