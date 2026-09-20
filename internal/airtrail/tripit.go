package airtrail

import (
	"encoding/json"
	"strings"
	"time"
)

// airObject is one air reservation of a TripIt v2 trip detail.
type airObject struct {
	IsClientTraveler   string    `json:"is_client_traveler"`
	SupplierConfNum    string    `json:"supplier_conf_num"`
	BookingSiteConfNum string    `json:"booking_site_conf_num"`
	BookingSiteName    string    `json:"booking_site_name"`
	Traveler           travelers `json:"Traveler"`
	Segment            segments  `json:"Segment"`
}

// traveler is one person on a reservation. TripIt gives a name only for a
// traveler that the booking named, so an entry can hold a ticket number and
// nothing else.
type traveler struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// name returns the full name of the traveler, and an empty string when
// TripIt gives no name.
func (t traveler) name() string {
	return strings.TrimSpace(strings.TrimSpace(t.FirstName) + " " + strings.TrimSpace(t.LastName))
}

// titles are the words that an airline writes in place of a given name.
var titles = map[string]bool{"mr": true, "mrs": true, "ms": true, "miss": true, "dr": true}

// isHolder reports whether this traveler is the account holder.
//
// An exact match on the whole name is too strict, because each airline
// writes the name its own way. The same person reaches one archive as
// "Aaron Spruit", "AARON CHRISTOPHER SPRUIT", "Aaronc Spruit", "MR Spruit"
// and "C Spruit Cntrl-". The last name of the account must appear as a word
// of the last name of the traveler, and then the given name must be a
// title, or share a start with the given name of the account.
//
// The rule leans toward a match. A traveler wrongly counted as the account
// holder loses a guest name; a companion wrongly counted as a guest puts the
// owner of the archive on their own flight twice.
func (t traveler) isHolder(holder profile) bool {
	if !hasWord(t.LastName, firstWord(holder.LastName)) {
		return false
	}
	given := firstWord(t.FirstName)
	if given == "" || titles[strings.ToLower(given)] {
		return true
	}
	return sharesStart(given, firstWord(holder.FirstName))
}

// hasWord reports whether want is one of the words of name. An airline
// writes a middle initial or a company code into the last name field, so
// the name of the account is a word of that field and not the whole of it.
func hasWord(name, want string) bool {
	if want == "" {
		return false
	}
	for _, word := range strings.Fields(name) {
		if strings.EqualFold(word, want) {
			return true
		}
	}
	return false
}

// sharesStart reports whether the shorter of two names is the start of the
// longer one. It needs 3 letters, so one initial matches no one.
func sharesStart(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 3 && strings.HasPrefix(b, a)
}

// firstWord returns the first word of a name, so a middle name written into
// the first name field does not stop a match.
func firstWord(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// profile is one TripIt account named on a trip. The Traveler list of a
// reservation holds the account holder as well as each companion, and it
// gives no flag to tell them apart, so the name of the profile does it.
type profile struct {
	IsClient  string `json:"is_client"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// profiles holds every account named on a trip. A trip that another
// traveler shares names each of them, so the list picks the account that
// owns this archive.
type profiles []profile

func (p *profiles) UnmarshalJSON(data []byte) error {
	return unmarshalOneOrMany(data, (*[]profile)(p))
}

// holder returns the account that owns this archive, which TripIt marks
// with is_client. The zero profile means that no profile carries the mark,
// and then no traveler counts as the account holder.
func (p profiles) holder() profile {
	for _, one := range p {
		if one.IsClient == "true" {
			return one
		}
	}
	return profile{}
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
	OperatingAirlineCode  string   `json:"operating_airline_code"`
	OperatingFlightNumber string   `json:"operating_flight_number"`
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

// travelers holds the people on a reservation, with the same one-or-many
// shape.
type travelers []traveler

func (t *travelers) UnmarshalJSON(data []byte) error {
	return unmarshalOneOrMany(data, (*[]traveler)(t))
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
