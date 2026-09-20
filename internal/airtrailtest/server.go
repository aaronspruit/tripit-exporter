// Package airtrailtest runs a fake AirTrail instance, so a test drives the
// sync without a real AirTrail.
package airtrailtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
)

// APIKey is the key that the fake server accepts.
const APIKey = "test-api-key"

// Server is a fake AirTrail instance. New starts it, and the test closes it.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	flights  map[int64]map[string]any
	nextID   int64
	failFrom map[string]int
	known    map[string]map[string]bool
	rejects  bool
	saves    int
	deletes  int
}

// New starts a fake AirTrail instance with no flights.
func New() *Server {
	s := &Server{
		flights:  make(map[int64]map[string]any),
		nextID:   1,
		failFrom: make(map[string]int),
		known:    make(map[string]map[string]bool),
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// RejectKey makes every request answer 401, as AirTrail does for a key that
// a person revoked.
func (s *Server) RejectKey() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejects = true
}

// FailFlight makes a save of a flight that leaves the airport code answer
// 400, as AirTrail does for a code that it does not hold.
func (s *Server) FailFlight(from string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failFrom[from] = status
}

// Hold makes the server accept one code of a field, and refuse every other
// code of that field the way AirTrail refuses a code its own table does not
// hold: it rejects the whole flight, and not the field alone. A field that
// Hold never names accepts every code.
func (s *Server) Hold(field string, codes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := make(map[string]bool, len(codes))
	for _, code := range codes {
		held[code] = true
	}
	s.known[field] = held
}

// Flights returns the saved flights, in id order.
func (s *Server) Flights() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.flights))
	for id := range s.flights {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.flights[id])
	}
	return out
}

// Counts returns how many saves and deletes the server answered.
func (s *Server) Counts() (saves, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves, s.deletes
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rejects := s.rejects
	s.mu.Unlock()
	if rejects || r.Header.Get("Authorization") != "Bearer "+APIKey {
		write(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		write(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}

	switch r.URL.Path {
	case "/api/flight/save":
		s.save(w, body)
	case "/api/flight/delete":
		s.delete(w, body)
	default:
		write(w, http.StatusNotFound, map[string]any{"success": false, "message": "Not found"})
	}
}

func (s *Server) save(w http.ResponseWriter, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++

	from, _ := body["from"].(string)
	if status, fail := s.failFrom[from]; fail {
		write(w, status, map[string]any{"success": false, "message": "Invalid departure airport"})
		return
	}
	for _, field := range []string{"airline", "aircraft"} {
		held, checked := s.known[field]
		code, sent := body[field].(string)
		if !checked || !sent || held[code] {
			continue
		}
		write(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Invalid " + field,
		})
		return
	}

	id := int64(0)
	if raw, ok := body["id"].(float64); ok {
		id = int64(raw)
	}
	if id != 0 {
		if _, known := s.flights[id]; !known {
			write(w, http.StatusNotFound, map[string]any{"success": false, "message": "Flight not found"})
			return
		}
		s.flights[id] = body
		write(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	id = s.nextID
	s.nextID++
	s.flights[id] = body
	write(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (s *Server) delete(w http.ResponseWriter, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++

	raw, ok := body["id"].(float64)
	if !ok {
		write(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid id"})
		return
	}
	id := int64(raw)
	if _, known := s.flights[id]; !known {
		write(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Flight not found"})
		return
	}
	delete(s.flights, id)
	write(w, http.StatusOK, map[string]any{"success": true})
}

func write(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
