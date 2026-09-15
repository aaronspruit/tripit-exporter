package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	old := version
	version = "0.0.1"
	t.Cleanup(func() { version = old })

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, nil, strings.NewReader(""), &stdout, &stderr)

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

func TestRunNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, nil, strings.NewReader(""), &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, nil, strings.NewReader(""), &stdout, &stderr)

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
