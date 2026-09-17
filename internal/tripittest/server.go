// Package tripittest starts a fake TripIt HTTP server for tests. It serves
// the routes of docs/research.md.
package tripittest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Server is a fake TripIt HTTP server. Call New to start one, and Close it
// when the test is done.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	feed feedResponse

	unauthorizedRemaining int
	trips                 []json.RawMessage
	tripConfigs           map[string]*tripConfig
	rejectedSessions      map[string]bool
	paths                 []string
}

// tripConfig holds the responses of the detail route and the download
// route for one trip uuid. A nil detail or download means the route
// returns 404.
type tripConfig struct {
	detail      *string
	download    *string
	rateLimited bool
	blocked     bool
}

type feedResponse struct {
	status int
	body   string
}

// New starts a fake TripIt server. The caller must call Close.
func New() *Server {
	s := &Server{
		feed:             feedResponse{status: http.StatusOK},
		tripConfigs:      make(map[string]*tripConfig),
		rejectedSessions: make(map[string]bool),
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// SetFeed sets the status and the body that the feed route returns for
// every request until the next call to SetFeed.
func (s *Server) SetFeed(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feed = feedResponse{status: status, body: body}
}

// FeedURL returns the URL of the feed route for the given key, in the form
// that docs/research.md documents:
// https://www.tripit.com/feed/ical/private/<key>/tripit.ics
func (s *Server) FeedURL(key string) string {
	return s.URL + "/feed/ical/private/" + key + "/tripit.ics"
}

// SetUnauthorizedCount makes the next n requests to a web API v2 route
// return 401, before the server goes back to its normal responses. It
// applies to the profile route, the list route and the detail route alike,
// so a test can put it in front of any one of them.
func (s *Server) SetUnauthorizedCount(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unauthorizedRemaining = n
}

// SetTrips sets the trips that the list route pages through, page_size 50
// at a time.
func (s *Server) SetTrips(trips []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trips = trips
}

// SetTripDetail sets the body that the detail route returns for uuid.
func (s *Server) SetTripDetail(uuid, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trip(uuid).detail = &body
}

// SetDownload sets the ICS body that the download route returns for uuid,
// when the request carries a browser header.
func (s *Server) SetDownload(uuid, ics string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trip(uuid).download = &ics
}

// RateLimitTripDetail makes the detail route return 429 for uuid, so a test
// can put a rate limit in the middle of a backfill run.
func (s *Server) RateLimitTripDetail(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trip(uuid).rateLimited = true
}

// BlockDownload makes the download route return 403 for uuid, even to a
// request with the browser headers, as TripIt does when it blocks a client.
func (s *Server) BlockDownload(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trip(uuid).blocked = true
}

// RejectSession makes a web API v2 request that sends value as
// it_session_id, and no session_id, get 500. TripIt returns 500 for an
// it_session_id value that it does not accept.
func (s *Server) RejectSession(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectedSessions[value] = true
}

// RenewedSession returns the it_session_id value that the server sets in
// response to a request that sent value.
func RenewedSession(value string) string {
	return "renewed-" + value
}

// Paths returns the path of each request that the server received, in
// order.
func (s *Server) Paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// trip returns the tripConfig of uuid, and adds one when there is none. The
// caller holds s.mu.
func (s *Server) trip(uuid string) *tripConfig {
	c, ok := s.tripConfigs[uuid]
	if !ok {
		c = &tripConfig{}
		s.tripConfigs[uuid] = c
	}
	return c
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	s.mu.Lock()
	s.paths = append(s.paths, path)
	s.mu.Unlock()

	if strings.HasPrefix(path, "/api/v2/") && s.handleSession(w, r) {
		return
	}

	switch {
	case strings.HasPrefix(path, "/feed/ical/private/") && strings.HasSuffix(path, "/tripit.ics"):
		s.mu.Lock()
		resp := s.feed
		s.mu.Unlock()

		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))

	case path == "/api/v2/get/profile":
		if s.consumeUnauthorized(w) {
			return
		}
		writeJSON(w, `{"Profile":{}}`)

	case path == "/api/v2/list/trip":
		if s.consumeUnauthorized(w) {
			return
		}
		s.handleList(w, r)

	case strings.HasPrefix(path, "/api/v2/get/trip/uuid/"):
		if s.consumeUnauthorized(w) {
			return
		}
		s.handleDetail(w, path)

	case strings.HasPrefix(path, "/trip/download/uuid/"):
		s.handleDownload(w, r, path)

	default:
		http.NotFound(w, r)
	}
}

// handleSession applies the "Keep me signed in" rule of TripIt to a request
// that sends it_session_id and no session_id: the response sets a new
// session_id and a new it_session_id. A value that RejectSession names gets
// 500 instead. It returns true when it wrote the whole response.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) bool {
	remembered, err := r.Cookie("it_session_id")
	if err != nil {
		return false
	}
	if _, err := r.Cookie("session_id"); err == nil {
		return false
	}

	s.mu.Lock()
	rejected := s.rejectedSessions[remembered.Value]
	s.mu.Unlock()
	if rejected {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}

	http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "session-" + remembered.Value, Path: "/", HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "it_session_id", Value: RenewedSession(remembered.Value), Path: "/", MaxAge: 15 * 24 * 60 * 60, HttpOnly: true})
	return false
}

// consumeUnauthorized writes a 401 and returns true when the server still
// owes the caller an unauthorized response.
func (s *Server) consumeUnauthorized(w http.ResponseWriter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unauthorizedRemaining <= 0 {
		return false
	}
	s.unauthorizedRemaining--
	w.WriteHeader(http.StatusUnauthorized)
	return true
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	const pageSize = 50

	s.mu.Lock()
	trips := s.trips
	s.mu.Unlock()

	pageNum, err := strconv.Atoi(r.URL.Query().Get("page_num"))
	if err != nil || pageNum < 1 {
		pageNum = 1
	}

	maxPage := (len(trips) + pageSize - 1) / pageSize
	if maxPage == 0 {
		maxPage = 1
	}

	start := (pageNum - 1) * pageSize
	end := start + pageSize
	if start > len(trips) {
		start = len(trips)
	}
	if end > len(trips) {
		end = len(trips)
	}

	writeJSONValue(w, map[string]any{
		"page_num":  pageNum,
		"page_size": pageSize,
		"max_page":  maxPage,
		"Trip":      trips[start:end],
	})
}

func (s *Server) handleDetail(w http.ResponseWriter, path string) {
	uuid := strings.TrimPrefix(path, "/api/v2/get/trip/uuid/")
	if i := strings.IndexByte(uuid, '/'); i >= 0 {
		uuid = uuid[:i]
	}

	s.mu.Lock()
	c := s.trip(uuid)
	rateLimited, body := c.rateLimited, c.detail
	s.mu.Unlock()
	if rateLimited {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if body == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, *body)
}

// handleDownload returns 403 when the request has no browser header, as
// TripIt does. Referer is the discriminator: a bare HTTP client does not
// send one, and the fixed browser header set in internal/tripitweb does.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("Referer") == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	uuid := strings.TrimPrefix(path, "/trip/download/uuid/")
	if i := strings.IndexByte(uuid, '/'); i >= 0 {
		uuid = uuid[:i]
	}

	s.mu.Lock()
	c := s.trip(uuid)
	blocked, ics := c.blocked, c.download
	s.mu.Unlock()
	if blocked {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if ics == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = fmt.Fprint(w, *ics)
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, body)
}

func writeJSONValue(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
