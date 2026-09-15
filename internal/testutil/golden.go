// Package testutil holds test helpers shared across tripit-exporter packages.
package testutil

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files instead of comparing against them")

// Golden compares got with testdata/<name>.golden. Run the test with
// -update to write got to that file instead of comparing it.
func Golden(t *testing.T, name string, got []byte) {
	t.Helper()

	if *update {
		if err := writeGolden(name, got); err != nil {
			t.Fatalf("%v", err)
		}
		return
	}

	if err := compareGolden(name, got); err != nil {
		t.Error(err)
	}
}

// compareGolden holds the comparison, so a test can check the failure without
// a fake testing.T. It reports how got differs from the golden file, and nil
// when the two are the same.
func compareGolden(name string, got []byte) error {
	path := goldenPath(name)

	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("testutil: read %s: %w", path, err)
	}
	if string(got) != string(want) {
		return fmt.Errorf("golden mismatch for %s\n got: %q\nwant: %q", name, got, want)
	}
	return nil
}

func writeGolden(name string, got []byte) error {
	path := goldenPath(name)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("testutil: make %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, got, 0o644); err != nil {
		return fmt.Errorf("testutil: write %s: %w", path, err)
	}
	return nil
}

func goldenPath(name string) string {
	return filepath.Join("testdata", name+".golden")
}
