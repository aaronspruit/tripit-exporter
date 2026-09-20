// Package airtrail turns the TripIt v2 objects of the archive into AirTrail
// flights, and keeps an AirTrail instance in step with the archive.
package airtrail

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
)

// PlaceholderUserID is the user id that AirTrail replaces with the holder of
// the API key. It is the default, so a run needs no user id at all.
const PlaceholderUserID = "<USER_ID>"

// Flight is one body of POST /api/flight/save.
//
// AirTrail reads the date from Departure and the clock time from
// DepartureTime, and it merges the two in the timezone of its own airport
// record. A date with no matching time is therefore stored with no time, and
// AirTrail answers an arrival date with no arrival time with a 400.
type Flight struct {
	ID                int64       `json:"id,omitempty"`
	From              string      `json:"from"`
	To                string      `json:"to"`
	DatePrecision     string      `json:"datePrecision"`
	Departure         string      `json:"departure"`
	DepartureTime     *string     `json:"departureTime"`
	Arrival           *string     `json:"arrival"`
	ArrivalTime       *string     `json:"arrivalTime"`
	FlightNumber      *string     `json:"flightNumber"`
	Airline           *string     `json:"airline"`
	Aircraft          *string     `json:"aircraft"`
	DepartureTerminal *string     `json:"departureTerminal"`
	DepartureGate     *string     `json:"departureGate"`
	ArrivalTerminal   *string     `json:"arrivalTerminal"`
	ArrivalGate       *string     `json:"arrivalGate"`
	Note              *string     `json:"note"`
	Passengers        []Passenger `json:"passengers"`
}

// Passenger is one entry of the passengers list of a flight. Exactly one of
// UserID and GuestName holds a value: AirTrail refuses a passenger that
// holds neither. The traveler of this account comes first, because AirTrail
// replaces the user id of the first passenger when it is the placeholder.
type Passenger struct {
	UserID     *string `json:"userId"`
	GuestName  *string `json:"guestName"`
	SeatNumber *string `json:"seatNumber"`
	SeatClass  *string `json:"seatClass"`
}

// Hash is the SHA-256 of the flight without its id, in hexadecimal. The sync
// stores it beside the AirTrail flight id and sends nothing while it matches,
// so an edit that a person makes in AirTrail stays until TripIt changes the
// same flight.
func (f Flight) Hash() string {
	f.ID = 0
	data, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Build returns one flight for each air segment of each trip, keyed by the
// TripIt segment UUID, and a warning for each segment it skips or cannot
// fill. The result is the whole wanted state of AirTrail, so the sync
// deletes a flight whose key is absent from it.
func Build(trips map[string]*archive.Trip, userID string, codes Codes) (map[string]Flight, []string) {
	if userID == "" {
		userID = PlaceholderUserID
	}
	flights := make(map[string]Flight)
	var warnings []string

	for _, trip := range orderedTrips(trips) {
		if len(trip.V2) == 0 || string(trip.V2) == "null" {
			continue
		}
		var detail struct {
			AirObject airObjects `json:"AirObject"`
			Profile   profiles   `json:"Profile"`
		}
		if err := json.Unmarshal(trip.V2, &detail); err != nil {
			warnings = append(warnings, fmt.Sprintf("trip %s: read the v2 object: %v", trip.UUID, err))
			continue
		}
		for _, air := range detail.AirObject {
			// A trip that another traveler shares is not the traveler's own
			// flight, so it does not belong in their AirTrail.
			if air.IsClientTraveler == "false" {
				continue
			}
			for _, seg := range air.Segment {
				if seg.IsHidden == "true" || seg.UUID == "" {
					continue
				}
				flight, warning := buildFlight(air, seg, userID, detail.Profile.holder(), codes)
				if warning != "" {
					warnings = append(warnings, fmt.Sprintf("trip %s: %s", trip.UUID, warning))
				}
				if flight == nil {
					continue
				}
				flights[seg.UUID] = *flight
			}
		}
	}
	sort.Strings(warnings)
	return flights, warnings
}

// buildFlight makes one flight from one TripIt segment. It returns a nil
// flight for a segment that holds too little to save, and a warning for
// anything it drops.
func buildFlight(air airObject, seg segment, userID string, holder profile, codes Codes) (*Flight, string) {
	from := strings.ToUpper(strings.TrimSpace(seg.StartAirportCode))
	to := strings.ToUpper(strings.TrimSpace(seg.EndAirportCode))
	if from == "" || to == "" {
		return nil, fmt.Sprintf("segment %s: no airport code, skipped", seg.UUID)
	}
	departure, departureTime := seg.StartDateTime.parts()
	if departure == "" {
		return nil, fmt.Sprintf("segment %s: no departure date, skipped", seg.UUID)
	}

	flight := Flight{
		From:              from,
		To:                to,
		DatePrecision:     "day",
		Departure:         departure,
		DepartureTime:     optional(departureTime),
		FlightNumber:      optional(flightNumber(seg)),
		DepartureTerminal: short(seg.StartTerminal, 10),
		DepartureGate:     short(seg.StartGate, 10),
		ArrivalTerminal:   short(seg.EndTerminal, 10),
		ArrivalGate:       short(seg.EndGate, 10),
		Note:              optional(note(air, seg)),
		Passengers:        passengers(air, seg, userID, holder),
	}

	var warning string
	arrival, arrivalTime := seg.EndDateTime.parts()
	// AirTrail rejects an arrival that is not after its departure, and a few
	// TripIt segments hold an arrival time that is earlier than the
	// departure. The flight keeps its departure and loses the arrival, which
	// leaves AirTrail to estimate the duration from the distance.
	if arrival != "" && arrivalTime != "" && !seg.EndDateTime.after(seg.StartDateTime) {
		warning = fmt.Sprintf("segment %s: TripIt gives an arrival before the departure, the flight keeps no arrival", seg.UUID)
		arrival, arrivalTime = "", ""
	}
	if arrival != "" && arrivalTime != "" {
		flight.Arrival = optional(arrival)
		flight.ArrivalTime = optional(arrivalTime)
	}

	if code := airlineICAO(seg, codes); code != "" {
		flight.Airline = optional(code)
	} else if name := strings.TrimSpace(seg.MarketingAirline); name != "" {
		warning = joinWarnings(warning, fmt.Sprintf("segment %s: no ICAO code for airline %q, the flight keeps no airline", seg.UUID, name))
	}

	if code := codes.Aircraft[strings.ToUpper(strings.TrimSpace(seg.Aircraft))]; code != "" {
		flight.Aircraft = optional(code)
	} else if name := strings.TrimSpace(seg.AircraftDisplayName); name != "" {
		warning = joinWarnings(warning, fmt.Sprintf("segment %s: no ICAO type code for aircraft %q (%s), the flight keeps no aircraft", seg.UUID, name, seg.Aircraft))
	}

	return &flight, warning
}

// passengers returns the traveler of this account first, then each
// companion that the reservation names as a guest. Only the first passenger
// gets the seat: TripIt holds every seat of the booking in one field, in no
// stated order, so a seat given to a companion could be the wrong one.
func passengers(air airObject, seg segment, userID string, holder profile) []Passenger {
	out := []Passenger{{
		UserID:     &userID,
		SeatNumber: short(firstSeat(seg.Seats), 5),
		SeatClass:  optional(seatClass(seg.ServiceClass)),
	}}
	class := optional(seatClass(seg.ServiceClass))
	for _, t := range air.Traveler {
		name := t.name()
		if name == "" || t.isHolder(holder) {
			continue
		}
		guest := short(name, 50)
		if guest == nil {
			continue
		}
		out = append(out, Passenger{GuestName: guest, SeatClass: class})
	}
	return out
}

// note returns the free text that AirTrail shows with the flight: the
// confirmation numbers of the reservation, and the flight number of the
// airline that really flies the leg.
//
// It never holds the TripIt operating_airline name, which is wrong for some
// reservations: TripIt names the code CO "North-Western Cargo
// International", and CO belonged to Continental Airlines.
func note(air airObject, seg segment) string {
	var lines []string
	if conf := strings.TrimSpace(air.SupplierConfNum); conf != "" {
		lines = append(lines, "Confirmation: "+conf)
	}
	if conf := strings.TrimSpace(air.BookingSiteConfNum); conf != "" && conf != strings.TrimSpace(air.SupplierConfNum) {
		site := strings.TrimSpace(air.BookingSiteName)
		if site == "" {
			site = "Booking site"
		}
		lines = append(lines, site+": "+conf)
	}
	code := strings.ToUpper(strings.TrimSpace(seg.OperatingAirlineCode))
	number := strings.TrimSpace(seg.OperatingFlightNumber)
	if code != "" && number != "" && code+number != flightNumber(seg) {
		lines = append(lines, "Operated as "+code+number)
	}
	text := strings.Join(lines, "\n")
	if len(text) > 1000 {
		return ""
	}
	return text
}

// airlineICAO returns the ICAO code of the marketing airline of seg. TripIt
// leaves the code field empty for some airlines and writes the IATA code in
// the name field instead, so the name is tried as a code.
func airlineICAO(seg segment, codes Codes) string {
	code := strings.ToUpper(strings.TrimSpace(seg.MarketingAirlineCode))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(seg.MarketingAirline))
	}
	return codes.Airline[code]
}

// flightNumber joins the airline code and the number, as AirTrail shows a
// flight number. It returns an empty string when TripIt gives no number.
func flightNumber(seg segment) string {
	number := strings.TrimSpace(seg.MarketingFlightNumber)
	if number == "" {
		return ""
	}
	full := strings.ToUpper(strings.TrimSpace(seg.MarketingAirlineCode)) + number
	if len(full) > 10 {
		return ""
	}
	return full
}

// seatClassPatterns maps a word of the TripIt service class to an AirTrail
// seat class. The order matters: a premium economy class holds both
// "premium" and "economy".
var seatClassPatterns = []struct {
	match string
	class string
}{
	{"premium", "economy+"},
	{"economy plus", "economy+"},
	{"business", "business"},
	{"first", "first"},
	{"coach", "economy"},
	{"economy", "economy"},
}

// seatClass maps the free text service class of TripIt, for example
// "Coach Class - M", to one of the AirTrail seat classes.
func seatClass(serviceClass string) string {
	text := strings.ToLower(serviceClass)
	for _, p := range seatClassPatterns {
		if strings.Contains(text, p.match) {
			return p.class
		}
	}
	return ""
}

// seatSeparators splits the TripIt seat field, which holds more than one
// seat for a booking that covers more than one traveler.
var seatSeparators = regexp.MustCompile(`[,;/]`)

// firstSeat returns the first seat of the TripIt seat field.
func firstSeat(seats string) string {
	return strings.TrimSpace(seatSeparators.Split(seats, 2)[0])
}

// orderedTrips returns the trips in UUID order, so a run always builds the
// same warnings in the same order.
func orderedTrips(trips map[string]*archive.Trip) []*archive.Trip {
	out := make([]*archive.Trip, 0, len(trips))
	for _, trip := range trips {
		out = append(out, trip)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UUID < out[j].UUID })
	return out
}

// joinWarnings joins two warnings of one segment into one line.
func joinWarnings(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

// optional returns nil for an empty string, so the JSON holds null.
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// short returns the trimmed value when it fits in max characters, and nil
// otherwise. AirTrail rejects a value that is too long.
func short(v string, max int) *string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > max {
		return nil
	}
	return &v
}
