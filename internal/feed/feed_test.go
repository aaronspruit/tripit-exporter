package feed

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

const validCalendar = "BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR\n"

func TestFetchExitCodes(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantExit    int
		wantLimited bool
	}{
		{"ok", http.StatusOK, validCalendar, 0, false},
		{"unauthorized", http.StatusUnauthorized, "", 1, false},
		{"forbidden", http.StatusForbidden, "", 1, false},
		{"notFound", http.StatusNotFound, "", 1, false},
		{"gone", http.StatusGone, "", 1, false},
		{"rateLimited", http.StatusTooManyRequests, "", 0, true},
		{"serverError", http.StatusInternalServerError, "", 2, false},
		{"badGateway", http.StatusBadGateway, "", 2, false},
		{"bodyDoesNotParse", http.StatusOK, "not a calendar", 2, false},
		{"bodyTooLarge", http.StatusOK, "BEGIN:VCALENDAR\n" + strings.Repeat("X", MaxBodySize) + "\nEND:VCALENDAR\n", 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tripittest.New()
			defer s.Close()
			s.SetFeed(tt.status, tt.body)

			calendar, rateLimited, err := Fetch(context.Background(), s.Client(), s.FeedURL("key"))

			if got := ExitCode(err); got != tt.wantExit {
				t.Fatalf("ExitCode() = %d, want %d (err = %v)", got, tt.wantExit, err)
			}
			if rateLimited != tt.wantLimited {
				t.Fatalf("rateLimited = %v, want %v", rateLimited, tt.wantLimited)
			}
			if tt.wantExit == 0 && !tt.wantLimited && string(calendar) != tt.body {
				t.Fatalf("calendar = %q, want %q", calendar, tt.body)
			}
			if (tt.wantExit != 0 || tt.wantLimited) && calendar != nil {
				t.Fatalf("calendar = %q, want nil", calendar)
			}
		})
	}
}

func TestCredentialErrorMessage(t *testing.T) {
	err := &CredentialError{StatusCode: http.StatusForbidden}
	if got, want := err.Error(), "feed: the server rejected the feed URL with status 403"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestFetchRedactsFeedURL(t *testing.T) {
	s := tripittest.New()
	feedURL := s.FeedURL("secret-key")
	s.Close() // closing first: the request now fails at the transport level

	_, _, err := Fetch(context.Background(), http.DefaultClient, feedURL)
	if err == nil {
		t.Fatal("want an error when the server is unreachable")
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("error %q holds the feed key", err.Error())
	}
	if strings.Contains(err.Error(), feedURL) {
		t.Fatalf("error %q holds the feed URL", err.Error())
	}
}
