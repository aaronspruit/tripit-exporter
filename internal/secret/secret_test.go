package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFromFile(t *testing.T) {
	old := dir
	dir = t.TempDir()
	t.Cleanup(func() { dir = old })

	if err := os.WriteFile(filepath.Join(dir, "tripit_feed_url"), []byte("https://example.com/feed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Read("tripit_feed_url", "TRIPIT_FEED_URL", map[string]string{"TRIPIT_FEED_URL": "env-value"})
	if want := "https://example.com/feed"; got != want {
		t.Fatalf("Read() = %q, want %q", got, want)
	}
}

func TestReadFromEnv(t *testing.T) {
	old := dir
	dir = t.TempDir()
	t.Cleanup(func() { dir = old })

	got := Read("tripit_feed_url", "TRIPIT_FEED_URL", map[string]string{"TRIPIT_FEED_URL": "env-value"})
	if want := "env-value"; got != want {
		t.Fatalf("Read() = %q, want %q", got, want)
	}
}

func TestReadMissing(t *testing.T) {
	old := dir
	dir = t.TempDir()
	t.Cleanup(func() { dir = old })

	got := Read("tripit_feed_url", "TRIPIT_FEED_URL", map[string]string{})
	if got != "" {
		t.Fatalf("Read() = %q, want empty", got)
	}
}
