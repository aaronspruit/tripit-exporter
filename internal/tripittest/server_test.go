package tripittest

import (
	"io"
	"net/http"
	"testing"
)

func TestServerFeedRoute(t *testing.T) {
	s := New()
	defer s.Close()

	s.SetFeed(http.StatusOK, "BEGIN:VCALENDAR\nEND:VCALENDAR\n")

	resp, err := http.Get(s.FeedURL("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "BEGIN:VCALENDAR\nEND:VCALENDAR\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestServerFeedRouteError(t *testing.T) {
	s := New()
	defer s.Close()

	s.SetFeed(http.StatusForbidden, "")

	resp, err := http.Get(s.FeedURL("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestServerUnknownRoute(t *testing.T) {
	s := New()
	defer s.Close()

	resp, err := http.Get(s.URL + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
