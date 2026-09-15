// Package testutil holds test helpers shared across tripit-exporter packages.
package testutil

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files instead of comparing against them")

// Golden compares got with testdata/<name>.golden. Run the test with
// -update to write got to that file instead of comparing it.
func Golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("testutil: make %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("testutil: write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testutil: read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s\n got: %q\nwant: %q", name, got, want)
	}
}
