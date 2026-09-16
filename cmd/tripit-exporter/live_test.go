//go:build live

package main

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/feed"
	"github.com/aaronspruit/tripit-exporter/internal/ics"
)

// TestLiveFeedFetch fetches the real TripIt feed and logs each event, so an
// operator can read the output and answer open questions 2 and 3 of
// docs/research.md: does a UID stay stable across an edit, and does a
// confirmation number show up in DESCRIPTION. Run it with:
//
//	TRIPIT_FEED_URL=... go test -tags live ./cmd/tripit-exporter/ -run TestLiveFeedFetch -v
func TestLiveFeedFetch(t *testing.T) {
	feedURL := os.Getenv("TRIPIT_FEED_URL")
	if feedURL == "" {
		t.Skip("TRIPIT_FEED_URL is empty")
	}

	calendar, rateLimited, err := feed.Fetch(context.Background(), http.DefaultClient, feedURL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rateLimited {
		t.Skip("the feed rate-limited this run")
	}

	events, err := ics.ParseEvents(calendar)
	if err != nil {
		t.Fatalf("ParseEvents: %v", err)
	}

	for _, e := range events {
		t.Logf("UID=%s", e.UID())
		for _, p := range e.Properties {
			t.Logf("  %s", p.Raw)
		}
	}
}
