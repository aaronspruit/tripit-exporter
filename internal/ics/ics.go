// Package ics reads and writes the VEVENT blocks of an ICS calendar. It
// keeps each property as raw text, with its parameters and its order, so
// the writer can produce the same content that the reader read.
package ics

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Property is one unfolded content line of a VEVENT, with no line
// terminator, for example "DTSTART;VALUE=DATE:20260615".
type Property struct {
	Name string
	Raw  string
}

// Value returns the part of the property after its first colon.
func (p Property) Value() string {
	if i := strings.IndexByte(p.Raw, ':'); i >= 0 {
		return p.Raw[i+1:]
	}
	return ""
}

// Event is one VEVENT block. Properties holds every property in the order
// that the reader read it.
type Event struct {
	Properties []Property
}

// Get returns the first property with the given name.
func (e Event) Get(name string) (Property, bool) {
	for _, p := range e.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return Property{}, false
}

// UID returns the value of the UID property, or "" if the event has none.
func (e Event) UID() string {
	p, _ := e.Get("UID")
	return p.Value()
}

// DTStart returns the raw DTSTART property (with its parameters), or "" if
// the event has none.
func (e Event) DTStart() string {
	p, _ := e.Get("DTSTART")
	return p.Raw
}

// Equal reports whether e and other hold the same properties in the same
// order, ignoring DTSTAMP. A fetch that only changes DTSTAMP therefore
// counts as no change.
func (e Event) Equal(other Event) bool {
	a := withoutDTStamp(e.Properties)
	b := withoutDTStamp(other.Properties)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func withoutDTStamp(props []Property) []Property {
	out := make([]Property, 0, len(props))
	for _, p := range props {
		if p.Name != "DTSTAMP" {
			out = append(out, p)
		}
	}
	return out
}

// ParseEvents reads every VEVENT block out of an ICS calendar. It accepts
// LF and CRLF line endings, and it joins folded lines before it splits a
// line into a property.
func ParseEvents(data []byte) ([]Event, error) {
	lines := unfold(data)

	var events []Event
	var current *Event
	for _, line := range lines {
		switch {
		case line == "BEGIN:VEVENT":
			current = &Event{}
		case line == "END:VEVENT":
			if current == nil {
				return nil, fmt.Errorf("ics: END:VEVENT with no matching BEGIN:VEVENT")
			}
			events = append(events, *current)
			current = nil
		case current != nil && line != "":
			current.Properties = append(current.Properties, Property{Name: propertyName(line), Raw: line})
		}
	}
	if current != nil {
		return nil, fmt.Errorf("ics: BEGIN:VEVENT with no matching END:VEVENT")
	}
	return events, nil
}

func propertyName(line string) string {
	if i := strings.IndexAny(line, ";:"); i >= 0 {
		return line[:i]
	}
	return line
}

// unfold splits data into logical lines: it accepts LF and CRLF, and it
// joins a line that starts with a space or a tab onto the line before it.
func unfold(data []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// WriteEvent writes one VEVENT block: CRLF line endings, each property
// folded at 75 octets, and never split inside a multi-byte UTF-8 character.
func WriteEvent(w io.Writer, e Event) error {
	if _, err := io.WriteString(w, "BEGIN:VEVENT\r\n"); err != nil {
		return err
	}
	for _, p := range e.Properties {
		if err := foldLine(w, p.Raw); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "END:VEVENT\r\n")
	return err
}

// WriteCalendar wraps events in a VCALENDAR block and writes each of them
// with WriteEvent.
func WriteCalendar(w io.Writer, events []Event) error {
	if _, err := io.WriteString(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//tripit-exporter//EN\r\n"); err != nil {
		return err
	}
	for _, e := range events {
		if err := WriteEvent(w, e); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "END:VCALENDAR\r\n")
	return err
}

// EventBytes renders one VEVENT block the way archive stores it: a single
// self-contained string with CRLF line endings.
func EventBytes(e Event) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteEvent(&buf, e); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const foldLimit = 75

// foldLine writes one property as one or more folded lines: the first line
// holds up to 75 octets, and each continuation line starts with one space
// and holds up to 74 more octets. It never splits a multi-byte UTF-8
// character, because a byte in the middle of one has its top two bits set
// to "10".
func foldLine(w io.Writer, line string) error {
	b := []byte(line)
	first := true
	for {
		limit := foldLimit
		if !first {
			limit = foldLimit - 1
		}

		n := limit
		if n > len(b) {
			n = len(b)
		}
		for n > 0 && n < len(b) && b[n]&0xC0 == 0x80 {
			n--
		}
		if n == 0 {
			// Only reachable with invalid UTF-8 (75+ continuation bytes in
			// a row). Fall back to a hard split so the loop still makes
			// progress.
			n = limit
		}

		if !first {
			if _, err := io.WriteString(w, " "); err != nil {
				return err
			}
		}
		if _, err := w.Write(b[:n]); err != nil {
			return err
		}
		b = b[n:]
		first = false

		if len(b) == 0 {
			break
		}
		if _, err := io.WriteString(w, "\r\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}
