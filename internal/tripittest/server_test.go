package tripittest

import (
	"encoding/json"
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

func TestServerProfileRoute(t *testing.T) {
	s := New()
	defer s.Close()

	resp, err := http.Get(s.URL + "/api/v2/get/profile")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServerUnauthorizedCountAppliesOnceThenClears(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetUnauthorizedCount(1)

	first, err := http.Get(s.URL + "/api/v2/get/profile")
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusUnauthorized)
	}

	second, err := http.Get(s.URL + "/api/v2/get/profile")
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want %d", second.StatusCode, http.StatusOK)
	}
}

func TestServerListTripRoutePages(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetTrips([]json.RawMessage{json.RawMessage(`{"uuid":"a"}`), json.RawMessage(`{"uuid":"b"}`)})

	resp, err := http.Get(s.URL + "/api/v2/list/trip?page_num=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		MaxPage int               `json:"max_page"`
		Trip    []json.RawMessage `json:"Trip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MaxPage != 1 || len(body.Trip) != 2 {
		t.Fatalf("body = %+v, want max_page 1 and 2 trips", body)
	}
}

func TestServerGetTripDetailRoute(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetTripDetail("abc", `{"Trip":{"uuid":"abc"}}`)

	resp, err := http.Get(s.URL + "/api/v2/get/trip/uuid/abc/include_objects/true")
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
	if string(body) != `{"Trip":{"uuid":"abc"}}` {
		t.Fatalf("body = %q", body)
	}
}

func TestServerGetTripDetailRouteMissing(t *testing.T) {
	s := New()
	defer s.Close()

	resp, err := http.Get(s.URL + "/api/v2/get/trip/uuid/missing/include_objects/true")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestServerRateLimitTripDetailRoute(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetTripDetail("abc", `{"Trip":{"uuid":"abc"}}`)
	s.RateLimitTripDetail("abc")

	resp, err := http.Get(s.URL + "/api/v2/get/trip/uuid/abc/include_objects/true")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
}

func TestServerDownloadRouteMissingTrip(t *testing.T) {
	s := New()
	defer s.Close()

	req, err := http.NewRequest(http.MethodGet, s.URL+"/trip/download/uuid/missing/missing.ics", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Referer", s.URL+"/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
