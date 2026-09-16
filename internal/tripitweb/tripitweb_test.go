package tripitweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

func TestNormalizeListSingleObjectGivesArrayOfOne(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"object", `{"uuid":"a"}`, 1},
		{"array of two", `[{"uuid":"a"},{"uuid":"b"}]`, 2},
		{"array of one", `[{"uuid":"a"}]`, 1},
		{"empty array", `[]`, 0},
		{"null", `null`, 0},
		{"empty", ``, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeList(json.RawMessage(tt.in))
			if len(got) != tt.want {
				t.Fatalf("NormalizeList(%q) has %d items, want %d", tt.in, len(got), tt.want)
			}
		})
	}
}

func TestDedupeByUUIDKeepsOneOfEachSharedPlan(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"uuid":"a","name":"first"}`),
		json.RawMessage(`{"uuid":"b"}`),
		json.RawMessage(`{"uuid":"a","name":"duplicate from a shared trip"}`),
	}

	got := DedupeByUUID(items)

	if len(got) != 2 {
		t.Fatalf("DedupeByUUID() has %d items, want 2: %v", len(got), got)
	}
	if UUIDField(got[0]) != "a" || UUIDField(got[1]) != "b" {
		t.Fatalf("DedupeByUUID() = %v, want uuid a then uuid b", got)
	}
}

func TestListTripsPagesUpToMaxPage(t *testing.T) {
	s := tripittest.New()
	defer s.Close()

	var trips []json.RawMessage
	for i := 0; i < 120; i++ {
		trips = append(trips, json.RawMessage(`{"uuid":"trip-`+strconv.Itoa(i)+`"}`))
	}
	s.SetTrips(trips)

	var slept []time.Duration
	client := &Client{BaseURL: s.URL, Sleep: func(d time.Duration) { slept = append(slept, d) }}
	got, err := client.ListTrips(context.Background(), "exclude_types=weather&past=true&traveler=all")
	if err != nil {
		t.Fatalf("ListTrips: %v", err)
	}
	if len(got) != 120 {
		t.Fatalf("ListTrips() has %d items, want 120 (page_size 50 across 3 pages)", len(got))
	}
	if len(slept) != 2 || slept[0] != pace || slept[1] != pace {
		t.Fatalf("slept %v, want one wait of %v between each two of the 3 pages", slept, pace)
	}
}

func TestUnauthorizedRetrySucceeds(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetUnauthorizedCount(1)

	var slept []time.Duration
	client := &Client{BaseURL: s.URL, Sleep: func(d time.Duration) { slept = append(slept, d) }}

	if err := client.Profile(context.Background()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if len(slept) != 2 || slept[0] != retryDelay || slept[1] != pace {
		t.Fatalf("slept %v, want the retry delay %v, then the pace %v", slept, retryDelay, pace)
	}
}

func TestTwoUnauthorizedResponsesReturnAuthError(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetUnauthorizedCount(2)

	client := &Client{BaseURL: s.URL, Sleep: func(time.Duration) {}}

	err := client.Profile(context.Background())
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode(%v) = %d, want 1", err, ExitCode(err))
	}
}

func TestDownloadWithoutBrowserHeadersGets403(t *testing.T) {
	s := tripittest.New()
	defer s.Close()

	resp, err := http.Get(s.URL + "/trip/download/uuid/abc/abc.ics")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestDownloadICSWithBrowserHeadersSucceeds(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetDownload("abc", "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")

	client := &Client{BaseURL: s.URL}
	got, err := client.DownloadICS(context.Background(), "abc", "abc.ics")
	if err != nil {
		t.Fatalf("DownloadICS: %v", err)
	}
	if string(got) != "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n" {
		t.Fatalf("DownloadICS() = %q", got)
	}
}

func TestDownloadICSMissingTripGivesError(t *testing.T) {
	s := tripittest.New()
	defer s.Close()

	client := &Client{BaseURL: s.URL}
	_, err := client.DownloadICS(context.Background(), "missing", "missing.ics")
	if err == nil {
		t.Fatal("DownloadICS() = nil error for a trip the server has no download for")
	}
	var downloadErr *DownloadError
	if !errors.As(err, &downloadErr) || downloadErr.Status != http.StatusNotFound {
		t.Fatalf("DownloadICS() error = %v, want a *DownloadError with status 404", err)
	}
}

func TestDownloadICSBlockedGivesDownloadError(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetDownload("abc", "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	s.BlockDownload("abc")

	client := &Client{BaseURL: s.URL}
	_, err := client.DownloadICS(context.Background(), "abc", "abc.ics")
	var downloadErr *DownloadError
	if !errors.As(err, &downloadErr) || downloadErr.Status != http.StatusForbidden {
		t.Fatalf("DownloadICS() error = %v, want a *DownloadError with status 403", err)
	}
	if downloadErr.Error() != "tripitweb: download trip abc: unexpected status 403" {
		t.Fatalf("Error() = %q", downloadErr.Error())
	}
}

func TestStalledRequestRetriesThenRateLimits(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()

	old := requestTimeout
	requestTimeout = 50 * time.Millisecond
	defer func() { requestTimeout = old }()

	var slept []time.Duration
	client := &Client{BaseURL: s.URL, Sleep: func(d time.Duration) { slept = append(slept, d) }}
	err := client.Profile(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Profile() error = %v, want context.DeadlineExceeded", err)
	}
	if ExitCode(err) != 0 {
		t.Fatalf("ExitCode(%v) = %d, want 0", err, ExitCode(err))
	}
	// Each retry waits its throttle wait, then the pace.
	if len(slept) != 2*len(throttleWaits) || slept[0] != throttleWaits[0] || slept[len(slept)-2] != throttleWaits[len(throttleWaits)-1] {
		t.Fatalf("slept %v, want each of %v followed by the pace", slept, throttleWaits)
	}
}

func TestGetTripDetailReturnsBodyAsRead(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetTripDetail("abc", `{"Trip":{"uuid":"abc"}}`)

	client := &Client{BaseURL: s.URL}
	got, err := client.GetTripDetail(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetTripDetail: %v", err)
	}
	if string(got) != `{"Trip":{"uuid":"abc"}}` {
		t.Fatalf("GetTripDetail() = %q", got)
	}
}

func TestRateLimitedTripDetailExitsZero(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.RateLimitTripDetail("abc")

	client := &Client{BaseURL: s.URL, Sleep: func(time.Duration) {}}
	_, err := client.GetTripDetail(context.Background(), "abc")
	if ExitCode(err) != 0 {
		t.Fatalf("ExitCode(%v) = %d, want 0", err, ExitCode(err))
	}
}

func TestIsProtocolError(t *testing.T) {
	if !isProtocolError(errors.New("http2: server sent GOAWAY and closed the connection; ERR_HTTP2_PROTOCOL_ERROR")) {
		t.Fatal("isProtocolError() = false for a protocol error")
	}
	if isProtocolError(errors.New("connection refused")) {
		t.Fatal("isProtocolError() = true for an unrelated network error")
	}
}

// resetServer resets the connection of the first n requests, then returns
// 200 with an empty JSON object.
func resetServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reset := n > 0
		n--
		mu.Unlock()
		if !reset {
			_, _ = w.Write([]byte("{}"))
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		// A zero linger time makes Close send a TCP reset.
		_ = conn.(*net.TCPConn).SetLinger(0)
		_ = conn.Close()
	}))
}

func TestConnectionResetWaitsThenSucceeds(t *testing.T) {
	s := resetServer(t, 1)
	defer s.Close()

	var slept []time.Duration
	var logged []string
	client := &Client{
		BaseURL: s.URL,
		Sleep:   func(d time.Duration) { slept = append(slept, d) },
		Logf:    func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	if err := client.Profile(context.Background()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if len(slept) != 2 || slept[0] != throttleWaits[0] || slept[1] != pace {
		t.Fatalf("slept %v, want %v, then the pace %v", slept, throttleWaits[0], pace)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "waiting 1m0s") {
		t.Fatalf("logged %q, want one line about the wait", logged)
	}
}

func TestConnectionResetEveryTimeExitsZero(t *testing.T) {
	s := resetServer(t, len(throttleWaits)+1)
	defer s.Close()

	client := &Client{BaseURL: s.URL, Sleep: func(time.Duration) {}}
	err := client.Profile(context.Background())
	var rateLimitErr *RateLimitedError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("Profile() error = %v, want a *RateLimitedError", err)
	}
	if ExitCode(err) != 0 {
		t.Fatalf("ExitCode(%v) = %d, want 0", err, ExitCode(err))
	}
}

func TestVerboseLogsRequestAndResponseWithoutSecrets(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "bm_sz", Value: "set-cookie-secret"})
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>Access Denied</html>"))
	}))
	defer s.Close()

	var logged []string
	client := &Client{
		BaseURL: s.URL,
		Cookie:  "JSESSIONID=cookie-secret",
		Verbose: true,
		Logf:    func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	_, _ = client.DownloadICS(context.Background(), "abc", "abc.ics")

	out := strings.Join(logged, "\n")
	for _, want := range []string{"-> GET " + s.URL + "/trip/download/uuid/abc/abc.ics", "Cookie: <hidden, 24 bytes>", "Referer: ", "<- HTTP/1.1 403", "Set-Cookie: bm_sz=<hidden>", "Access Denied"} {
		if !strings.Contains(out, want) {
			t.Fatalf("verbose output lacks %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"cookie-secret", "set-cookie-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("verbose output holds %q:\n%s", secret, out)
		}
	}
}

func TestVerboseLogsRequestError(t *testing.T) {
	var logged []string
	client := &Client{
		BaseURL: "http://127.0.0.1:1",
		Verbose: true,
		Logf:    func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	_ = client.Profile(context.Background())
	if !strings.Contains(strings.Join(logged, "\n"), "<- error after") {
		t.Fatalf("verbose output lacks the request error: %q", logged)
	}
}

func TestRateLimitedErrorMessage(t *testing.T) {
	err := &RateLimitedError{cause: errors.New("boom")}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Error() = %q, want it to mention the cause", err.Error())
	}
	if !errors.Is(errors.Unwrap(err), err.cause) {
		t.Fatal("Unwrap() did not return the cause")
	}

	bare := &RateLimitedError{}
	if bare.Error() == "" {
		t.Fatal("Error() must not be empty with no cause")
	}
}

func TestAuthErrorMessage(t *testing.T) {
	if (&AuthError{}).Error() == "" {
		t.Fatal("Error() must not be empty")
	}
}

func TestExitCodeSuccess(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Fatalf("ExitCode(nil) = %d, want 0", ExitCode(nil))
	}
}

func TestExitCodeOtherError(t *testing.T) {
	if ExitCode(errors.New("boom")) != 2 {
		t.Fatalf("ExitCode(boom) = %d, want 2", ExitCode(errors.New("boom")))
	}
}
