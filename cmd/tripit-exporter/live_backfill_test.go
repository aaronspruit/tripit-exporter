//go:build live

package main

import (
	"context"
	"os"
	"testing"

	"github.com/aaronspruit/tripit-exporter/internal/tripitweb"
)

// TestLiveBackfillDownload downloads one trip's calendar with the fixed
// browser header set that internal/tripitweb sends, so an operator can read
// the result and answer open questions 1 and 2 of docs/research.md: does
// the download need every header in that set, and does TripIt read the
// file name at the end of the URL. Run it with:
//
//	TRIPIT_COOKIE=... TRIPIT_TRIP_UUID=... go test -tags live ./cmd/tripit-exporter/ -run TestLiveBackfillDownload -v
func TestLiveBackfillDownload(t *testing.T) {
	cookie := os.Getenv("TRIPIT_COOKIE")
	uuid := os.Getenv("TRIPIT_TRIP_UUID")
	if cookie == "" || uuid == "" {
		t.Skip("TRIPIT_COOKIE or TRIPIT_TRIP_UUID is empty")
	}

	client := &tripitweb.Client{Cookie: cookie}
	ctx := context.Background()

	if err := client.Profile(ctx); err != nil {
		t.Fatalf("Profile: %v", err)
	}

	calendar, err := client.DownloadICS(ctx, uuid, uuid+".ics")
	if err != nil {
		t.Fatalf("DownloadICS with the fixed browser header set: %v", err)
	}
	t.Logf("download succeeded with the fixed header set, %d bytes", len(calendar))
	t.Log("to test question 2, run this test again with TRIPIT_TRIP_UUID set to a file name TripIt did not issue, and compare")
}
