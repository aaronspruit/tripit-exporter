package airtrail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aaronspruit/tripit-exporter/internal/archive"
)

// StateFile is the name of the sync state file in the output folder.
const StateFile = "airtrail-state.json"

// stateSchema is the version of the state file layout.
const stateSchema = 1

// State remembers which AirTrail flight came from which TripIt segment.
//
// It cannot live in the trip files. The archive deletes the trip file of a
// trip that left the feed, which is the moment the sync needs the AirTrail
// id to delete the flight.
type State struct {
	Schema  int                    `json:"schema"`
	Flights map[string]FlightState `json:"flights"`
}

// FlightState is the AirTrail flight of one TripIt segment.
type FlightState struct {
	// ID is the AirTrail flight id.
	ID int64 `json:"id"`
	// Hash is Flight.Hash of the body that made this flight. While it
	// matches, the sync sends nothing, so an edit made in AirTrail stays.
	Hash string `json:"hash"`
}

// LoadState reads the state file of dir. A missing file is not an error: it
// means that no run has written to AirTrail yet.
func LoadState(dir string) (*State, error) {
	state := &State{Schema: stateSchema, Flights: make(map[string]FlightState)}
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("airtrail: read %s: %w", StateFile, err)
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("airtrail: read %s: %w", StateFile, err)
	}
	if state.Flights == nil {
		state.Flights = make(map[string]FlightState)
	}
	state.Schema = stateSchema
	return state, nil
}

// Save writes the state file of dir, and writes nothing when the content
// did not change.
func (s *State) Save(dir string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("airtrail: encode %s: %w", StateFile, err)
	}
	return archive.WriteFileIfChanged(dir, StateFile, append(data, '\n'))
}

// keys returns the segment UUIDs of the state in order, so a run always
// deletes in the same order.
func (s *State) keys() []string {
	out := make([]string, 0, len(s.Flights))
	for uuid := range s.Flights {
		out = append(out, uuid)
	}
	sort.Strings(out)
	return out
}
