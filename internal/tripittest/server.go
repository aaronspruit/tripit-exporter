// Package tripittest starts a fake TripIt HTTP server for tests. It serves
// the routes of docs/research.md.
package tripittest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Server is a fake TripIt HTTP server. Call New to start one, and Close it
// when the test is done.
type Server struct {
	*httptest.Server

	mu   sync.Mutex
	feed feedResponse
}

type feedResponse struct {
	status int
	body   string
}

// New starts a fake TripIt server. The caller must call Close.
func New() *Server {
	s := &Server{feed: feedResponse{status: http.StatusOK}}
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

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/feed/ical/private/") && strings.HasSuffix(r.URL.Path, "/tripit.ics") {
		s.mu.Lock()
		resp := s.feed
		s.mu.Unlock()

		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
		return
	}

	http.NotFound(w, r)
}
