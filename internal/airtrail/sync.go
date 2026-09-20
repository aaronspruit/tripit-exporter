package airtrail

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Options are the settings of one sync run.
type Options struct {
	// Delete turns the delete step on. With it off, the sync adds and
	// updates, and it leaves a flight that TripIt no longer holds.
	Delete bool
}

// Result counts what one sync run changed.
type Result struct {
	Added     int
	Updated   int
	Deleted   int
	Unchanged int
	Failed    int
}

// Sync makes the flights of an AirTrail instance match the air segments of
// the archive, and keeps the state file of dir in step.
//
// A segment with no state entry is added. A segment whose hash matches its
// state entry sends no request at all, so a run that changes nothing in
// TripIt changes nothing in AirTrail, and an edit made in AirTrail stays. A
// segment whose hash differs replaces the flight of its stored id. A state
// entry with no segment is deleted, which covers a cancelled trip and a
// cancelled leg of a trip that goes ahead.
//
// A flight that AirTrail refuses makes a warning and leaves no state entry,
// so the next run tries it again. Only a rejected API key stops the run.
func Sync(ctx context.Context, client *Client, dir string, wanted map[string]Flight, opts Options) (Result, []string, error) {
	var result Result
	var warnings []string

	state, err := LoadState(dir)
	if err != nil {
		return result, warnings, err
	}

	for _, uuid := range sortedKeys(wanted) {
		flight := wanted[uuid]
		hash := flight.Hash()
		known, seen := state.Flights[uuid]
		if seen && known.Hash == hash {
			result.Unchanged++
			continue
		}
		if seen {
			flight.ID = known.ID
		}

		id, err := client.Save(ctx, flight)
		var authErr *AuthError
		if errors.As(err, &authErr) {
			return result, warnings, err
		}
		var missing *NotFoundError
		if errors.As(err, &missing) {
			// A person removed the flight in AirTrail, so add it again with
			// no id. Every other failure keeps the id: a retry with no id
			// would add a second copy of a flight that is still there.
			flight.ID = 0
			id, err = client.Save(ctx, flight)
			if errors.As(err, &authErr) {
				return result, warnings, err
			}
		}
		if err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("segment %s: %v", uuid, err))
			continue
		}

		state.Flights[uuid] = FlightState{ID: id, Hash: hash}
		if seen {
			result.Updated++
		} else {
			result.Added++
		}
		// The state file holds the only record of the new flight, so it is
		// written before the next request. A run that stops here adds no
		// second copy of the flight on the next run.
		if err := state.Save(dir); err != nil {
			return result, warnings, err
		}
	}

	if opts.Delete {
		for _, uuid := range state.keys() {
			if _, want := wanted[uuid]; want {
				continue
			}
			known := state.Flights[uuid]
			err := client.Delete(ctx, known.ID)
			var authErr *AuthError
			var missing *NotFoundError
			switch {
			case errors.As(err, &authErr):
				return result, warnings, err
			case err != nil && !errors.As(err, &missing):
				result.Failed++
				warnings = append(warnings, fmt.Sprintf("segment %s: delete AirTrail flight %d: %v", uuid, known.ID, err))
				continue
			}
			delete(state.Flights, uuid)
			result.Deleted++
			if err := state.Save(dir); err != nil {
				return result, warnings, err
			}
		}
	}

	if err := state.Save(dir); err != nil {
		return result, warnings, err
	}
	sort.Strings(warnings)
	return result, warnings, nil
}

// sortedKeys returns the keys of flights in order, so a run always writes in
// the same order.
func sortedKeys(flights map[string]Flight) []string {
	out := make([]string, 0, len(flights))
	for uuid := range flights {
		out = append(out, uuid)
	}
	sort.Strings(out)
	return out
}
