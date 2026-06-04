package data

import "testing"

// TestSnapshotValid ensures the embedded fallback decodes AND passes full
// schema validation — a malformed seed would ship a broken offline dashboard.
func TestSnapshotValid(t *testing.T) {
	ds, err := Snapshot()
	if err != nil {
		t.Fatalf("Snapshot decode: %v", err)
	}
	if len(ds.Repos) == 0 {
		t.Fatal("embedded snapshot has no repos")
	}
	if err := ds.Validate(); err != nil {
		t.Fatalf("embedded snapshot fails validation: %v", err)
	}
}
