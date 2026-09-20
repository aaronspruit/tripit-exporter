package airtrail

// Codes holds the two lookups that AirTrail needs and TripIt does not give.
// AirTrail matches an airline and an aircraft type by its ICAO code alone,
// with no IATA fallback, and TripIt gives an IATA code for both.
type Codes struct {
	// Airline maps an IATA airline code, for example "UA", to an ICAO
	// airline code, for example "UAL".
	Airline map[string]string
	// Aircraft maps an IATA aircraft type code, for example "73H", to an
	// ICAO type code, for example "B738".
	Aircraft map[string]string
}

// DefaultCodes returns the built-in tables. A code that no table holds makes
// a warning and an empty field, and never a guess: AirTrail accepts a wrong
// airline or a wrong aircraft without a word, so a guess would be invisible.
func DefaultCodes() Codes {
	return Codes{Airline: airlineICAOTable, Aircraft: aircraftICAOTable}
}

// airlineICAOTable maps an IATA airline code to an ICAO airline code.
var airlineICAOTable = map[string]string{
	"AA": "AAL", // American Airlines
	"AC": "ACA", // Air Canada
	"AS": "ASA", // Alaska Airlines
	"B6": "JBU", // JetBlue
	"BR": "EVA", // EVA Air
	"DJ": "DJB", // Air Djibouti
	"DL": "DAL", // Delta Air Lines
	"EI": "EIN", // Aer Lingus
	"HA": "HAL", // Hawaiian Airlines
	"KL": "KLM", // KLM
	"NW": "NWA", // Northwest Airlines
	"NZ": "ANZ", // Air New Zealand
	"QF": "QFA", // Qantas
	"RO": "ROT", // Tarom
	"UA": "UAL", // United Airlines
	"VA": "VOZ", // Virgin Australia
	"WP": "WSG", // Wasaya Airways
}

// aircraftICAOTable maps an IATA aircraft type code to an ICAO type code.
// A generic IATA code that names more than one type is absent on purpose:
// "777" covers every Boeing 777, "32S" covers four Airbus models, and
// "ATR", "ERJ", "DH8", "AT7" and "757" each cover a family.
var aircraftICAOTable = map[string]string{
	"223": "BCS3", // Airbus A220-300
	"319": "A319", // Airbus A319
	"320": "A320", // Airbus A320
	"321": "A321", // Airbus A321
	"32Q": "A21N", // Airbus A321neo
	"332": "A332", // Airbus A330-200
	"333": "A333", // Airbus A330-300
	"388": "A388", // Airbus A380-800
	"717": "B712", // Boeing 717-200
	"733": "B733", // Boeing 737-300
	"734": "B734", // Boeing 737-400
	"735": "B735", // Boeing 737-500
	"738": "B738", // Boeing 737-800
	"739": "B739", // Boeing 737-900
	"73H": "B738", // Boeing 737-800 with winglets
	"73J": "B739", // Boeing 737-900 with winglets
	"73W": "B737", // Boeing 737-700 with winglets
	"744": "B744", // Boeing 747-400
	"752": "B752", // Boeing 757-200
	"753": "B753", // Boeing 757-300
	"75W": "B752", // Boeing 757-200 with winglets
	"763": "B763", // Boeing 767-300
	"764": "B764", // Boeing 767-400
	"773": "B773", // Boeing 777-300
	"77W": "B77W", // Boeing 777-300ER
	"781": "B78X", // Boeing 787-10
	"7M9": "B39M", // Boeing 737 MAX 9
	"CR7": "CRJ7", // Canadair Regional Jet 700
	"CRA": "CRJ7", // Canadair Regional Jet 705
	"DH4": "DH8D", // Bombardier DHC-8-400 Dash 8
	"E70": "E170", // Embraer ERJ-170
	"E7W": "E75L", // Embraer E175, long wing
	"E90": "E190", // Embraer 190
	"EM2": "E120", // Embraer EMB-120 Brasilia
	"ER4": "E145", // Embraer ERJ-145
	"M83": "MD83", // McDonnell Douglas MD-83
}
