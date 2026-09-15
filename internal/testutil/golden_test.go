package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenMatch(t *testing.T) {
	Golden(t, "sample", []byte("hello\n"))
}

func TestCompareGoldenMismatch(t *testing.T) {
	err := compareGolden("sample", []byte("bad\n"))
	if err == nil {
		t.Fatal("compareGolden() returned no error for content that differs")
	}
	if !strings.Contains(err.Error(), "sample") {
		t.Errorf("error = %q, want it to name the golden file", err)
	}
}

func TestCompareGoldenMissingFile(t *testing.T) {
	if err := compareGolden("absent", []byte("hello\n")); err == nil {
		t.Fatal("compareGolden() returned no error for a golden file that does not exist")
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
