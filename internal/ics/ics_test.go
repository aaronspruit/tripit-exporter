package ics

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseEventsLineEndings(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"LF", "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:a@tripit.com\nEND:VEVENT\nEND:VCALENDAR\n"},
		{"CRLF", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@tripit.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := ParseEvents([]byte(tt.data))
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatalf("len(events) = %d, want 1", len(events))
			}
			if got := events[0].UID(); got != "a@tripit.com" {
				t.Fatalf("UID() = %q, want %q", got, "a@tripit.com")
			}
		})
	}
}

func TestParseEventsJoinsFoldedLines(t *testing.T) {
	data := "BEGIN:VEVENT\r\nDESCRIPTION:one two\r\n  three\r\nEND:VEVENT\r\n"
	events, err := ParseEvents([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := events[0].Get("DESCRIPTION")
	if !ok {
		t.Fatal("DESCRIPTION property not found")
	}
	if want := "DESCRIPTION:one two three"; p.Raw != want {
		t.Fatalf("DESCRIPTION raw = %q, want %q", p.Raw, want)
	}
}

func TestParseEventsUnmatchedBegin(t *testing.T) {
	if _, err := ParseEvents([]byte("BEGIN:VEVENT\nUID:a\n")); err == nil {
		t.Fatal("want error for unmatched BEGIN:VEVENT")
	}
}

func TestParseEventsUnmatchedEnd(t *testing.T) {
	if _, err := ParseEvents([]byte("UID:a\nEND:VEVENT\n")); err == nil {
		t.Fatal("want error for unmatched END:VEVENT")
	}
}

func TestWriteEventFolds75Octets(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"short", "short value"},
		{"exactly75", strings.Repeat("a", 75-len("DESCRIPTION:"))},
		{"long", strings.Repeat("a", 200)},
		{"multibyte", strings.Repeat("é", 60)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Event{Properties: []Property{{Name: "DESCRIPTION", Raw: "DESCRIPTION:" + tt.value}}}
			var buf bytes.Buffer
			if err := WriteEvent(&buf, e); err != nil {
				t.Fatal(err)
			}

			for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\r\n"), "\r\n") {
				if line == "BEGIN:VEVENT" || line == "END:VEVENT" {
					continue
				}
				if n := len(line); n > 75 {
					t.Fatalf("line %q has %d octets, want <= 75", line, n)
				}
				if len(line) > 0 && line[0]&0xC0 == 0x80 {
					t.Fatalf("line %q starts mid-rune", line)
				}
			}

			round, err := ParseEvents(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if got := round[0].Properties[0].Raw; got != "DESCRIPTION:"+tt.value {
				t.Fatalf("round trip = %q, want %q", got, "DESCRIPTION:"+tt.value)
			}
		})
	}
}

func TestEventDTStart(t *testing.T) {
	e := Event{Properties: []Property{{Name: "DTSTART", Raw: "DTSTART;VALUE=DATE:20260615"}}}
	if got, want := e.DTStart(), "DTSTART;VALUE=DATE:20260615"; got != want {
		t.Fatalf("DTStart() = %q, want %q", got, want)
	}
	if got := (Event{}).DTStart(); got != "" {
		t.Fatalf("DTStart() = %q, want empty for an event with no DTSTART", got)
	}
}

func TestEventEqualIgnoresDTStamp(t *testing.T) {
	a := Event{Properties: []Property{
		{Name: "UID", Raw: "UID:a@tripit.com"},
		{Name: "DTSTAMP", Raw: "DTSTAMP:20260101T000000Z"},
	}}
	b := Event{Properties: []Property{
		{Name: "UID", Raw: "UID:a@tripit.com"},
		{Name: "DTSTAMP", Raw: "DTSTAMP:20260102T000000Z"},
	}}
	if !a.Equal(b) {
		t.Fatal("Equal() = false, want true when only DTSTAMP differs")
	}

	c := Event{Properties: []Property{
		{Name: "UID", Raw: "UID:a@tripit.com"},
		{Name: "SUMMARY", Raw: "SUMMARY:changed"},
		{Name: "DTSTAMP", Raw: "DTSTAMP:20260102T000000Z"},
	}}
	if a.Equal(c) {
		t.Fatal("Equal() = true, want false when a non-DTSTAMP property differs")
	}
}

func TestWriteCalendar(t *testing.T) {
	var buf bytes.Buffer
	e := Event{Properties: []Property{{Name: "UID", Raw: "UID:a@tripit.com"}}}
	if err := WriteCalendar(&buf, []Event{e}); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if !strings.HasPrefix(got, "BEGIN:VCALENDAR\r\n") {
		t.Fatalf("calendar does not start with BEGIN:VCALENDAR: %q", got)
	}
	if !strings.HasSuffix(got, "END:VCALENDAR\r\n") {
		t.Fatalf("calendar does not end with END:VCALENDAR: %q", got)
	}
	if !strings.Contains(got, "BEGIN:VEVENT\r\nUID:a@tripit.com\r\nEND:VEVENT\r\n") {
		t.Fatalf("calendar does not hold the event: %q", got)
	}
}
