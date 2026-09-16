package ics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzWriteEventFold checks that WriteEvent never produces a line over 75
// octets and never splits a line inside a multi-byte UTF-8 character, for
// an arbitrary DESCRIPTION value.
func FuzzWriteEventFold(f *testing.F) {
	f.Add("short")
	f.Add(strings.Repeat("a", 200))
	f.Add(strings.Repeat("é", 100))
	f.Add(strings.Repeat("🎉", 40))
	f.Add("")

	f.Fuzz(func(t *testing.T, value string) {
		if !utf8.ValidString(value) {
			t.Skip("random bytes need not be valid UTF-8")
		}
		if strings.ContainsAny(value, "\r\n") {
			t.Skip("a raw content line never holds a literal CR or LF")
		}

		e := Event{Properties: []Property{{Name: "DESCRIPTION", Raw: "DESCRIPTION:" + value}}}
		out, err := EventBytes(e)
		if err != nil {
			t.Fatal(err)
		}

		for _, line := range strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n") {
			if n := len(line); n > 75 {
				t.Fatalf("line %q has %d octets, want <= 75", line, n)
			}
			if len(line) > 0 && line[0]&0xC0 == 0x80 {
				t.Fatalf("line %q starts mid-rune", line)
			}
		}

		round, err := ParseEvents(out)
		if err != nil {
			t.Fatal(err)
		}
		if got := round[0].Properties[0].Raw; got != "DESCRIPTION:"+value {
			t.Fatalf("round trip = %q, want %q", got, "DESCRIPTION:"+value)
		}
	})
}

// FuzzRoundTrip checks that reading an event, writing it, and reading it
// again gives the same events.
func FuzzRoundTrip(f *testing.F) {
	f.Add("BEGIN:VEVENT\r\nUID:a@tripit.com\r\nDTSTART:20260615T000000Z\r\nEND:VEVENT\r\n")
	f.Add("BEGIN:VEVENT\nUID:b@tripit.com\nDESCRIPTION:one\n two\nEND:VEVENT\n")

	f.Fuzz(func(t *testing.T, data string) {
		first, err := ParseEvents([]byte(data))
		if err != nil {
			t.Skip("not a well-formed VEVENT sequence")
		}

		var buf strings.Builder
		for _, e := range first {
			if err := WriteEvent(&buf, e); err != nil {
				t.Fatal(err)
			}
		}

		second, err := ParseEvents([]byte(buf.String()))
		if err != nil {
			t.Fatalf("re-parse after write: %v", err)
		}
		if len(first) != len(second) {
			t.Fatalf("len(second) = %d, want %d", len(second), len(first))
		}
		for i := range first {
			if !first[i].Equal(second[i]) || first[i].UID() != second[i].UID() {
				t.Fatalf("event %d changed: %+v != %+v", i, first[i], second[i])
			}
		}
	})
}
