package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenMatch(t *testing.T) {
	Golden(t, "sample", []byte("hello\n"))
}

func TestGoldenMismatch(t *testing.T) {
	fake := &testing.T{}
	Golden(fake, "sample", []byte("bad\n"))
	if !fake.Failed() {
		t.Fatal("Golden() did not fail on a mismatch")
	}
}

func TestGoldenUpdate(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	*update = true
	t.Cleanup(func() { *update = false })

	Golden(t, "generated", []byte("world\n"))

	got, err := os.ReadFile(filepath.Join(dir, "testdata", "generated.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "world\n" {
		t.Fatalf("golden file = %q, want %q", got, "world\n")
	}
}
