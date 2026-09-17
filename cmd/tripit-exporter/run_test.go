package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronspruit/tripit-exporter/internal/tripittest"
)

var testNow = time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

func TestRunVersion(t *testing.T) {
	old := version
	version = "0.0.1"
	t.Cleanup(func() { version = old })

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, nil, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := stdout.String(); got != "0.0.1\n" {
		t.Fatalf("stdout = %q, want %q", got, "0.0.1\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, nil, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Fatalf("stderr = %q, want it to name the subcommand", stderr.String())
	}
}

func TestEnvironMap(t *testing.T) {
	got := environMap([]string{"FOO=bar", "EMPTY=", "MALFORMED"})
	want := map[string]string{"FOO": "bar", "EMPTY": ""}

	if len(got) != len(want) {
		t.Fatalf("environMap() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("environMap()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

const testCalendar = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:trip-1@tripit.com\r\n" +
	"DTSTART;VALUE=DATE:20260615\r\n" +
	"DTEND;VALUE=DATE:20260619\r\n" +
	"DTSTAMP:20260620T000000Z\r\n" +
	"SUMMARY:Seattle, WA\r\n" +
	"DESCRIPTION:https://www.tripit.com/trip/show?id=111\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestRunFeedMissingFeedURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, map[string]string{"OUTPUT_DIR": t.TempDir()}, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "TRIPIT_FEED_URL") {
		t.Fatalf("stderr = %q, want it to name TRIPIT_FEED_URL", stderr.String())
	}
}

func TestOutputDirDefaultsToData(t *testing.T) {
	if got := outputDir(nil); got != "/data" {
		t.Fatalf("outputDir(nil) = %q, want /data", got)
	}
	if got := outputDir(map[string]string{"OUTPUT_DIR": "/archive"}); got != "/archive" {
		t.Fatalf("outputDir() = %q, want /archive", got)
	}
}

func TestRunFeedSuccessWritesArchive(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetFeed(http.StatusOK, testCalendar)

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run(nil, map[string]string{"TRIPIT_FEED_URL": s.FeedURL("key"), "OUTPUT_DIR": dir}, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "trip-1.json")); err != nil {
		t.Fatalf("trip file not written: %v", err)
	}
}

func TestRunFeedCredentialErrorExitsOne(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetFeed(http.StatusForbidden, "")

	var stdout, stderr bytes.Buffer
	code := run(nil, map[string]string{"TRIPIT_FEED_URL": s.FeedURL("key"), "OUTPUT_DIR": t.TempDir()}, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestRunFeedRateLimitedExitsZeroWithNoChange(t *testing.T) {
	s := tripittest.New()
	defer s.Close()
	s.SetFeed(http.StatusTooManyRequests, "")

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run(nil, map[string]string{"TRIPIT_FEED_URL": s.FeedURL("key"), "OUTPUT_DIR": dir}, strings.NewReader(""), &stdout, &stderr, testNow)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "trips")); !os.IsNotExist(err) {
		t.Fatalf("a rate limit must not touch the archive, trips dir err = %v", err)
	}
}

func TestRunFeedFailedFetchAfterGoodFetchChangesNothing(t *testing.T) {
	s := tripittest.New()
	defer s.Close()

	dir := t.TempDir()
	env := map[string]string{"TRIPIT_FEED_URL": s.FeedURL("key"), "OUTPUT_DIR": dir}

	s.SetFeed(http.StatusOK, testCalendar)
	var stdout, stderr bytes.Buffer
	if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 0 {
		t.Fatalf("first run: exit code = %d, stderr = %q", code, stderr.String())
	}
	before, err := os.ReadFile(filepath.Join(dir, "trips", "trip-1.json"))
	if err != nil {
		t.Fatal(err)
	}

	s.SetFeed(http.StatusInternalServerError, "")
	stdout.Reset()
	stderr.Reset()
	if code := run(nil, env, strings.NewReader(""), &stdout, &stderr, testNow); code != 2 {
		t.Fatalf("second run: exit code = %d, want 2", code)
	}

	after, err := os.ReadFile(filepath.Join(dir, "trips", "trip-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a failed fetch changed the archive")
	}
}
