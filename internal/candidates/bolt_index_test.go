package candidates

import (
	"context"
	"path/filepath"
	"testing"

	"dispatchkernel/internal/kernel"
)

func TestBoltIndexPersistsCandidatesAndUpdatesSpatialIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.db")
	index, err := OpenBoltIndex(path, 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	origin := kernel.Location{Latitude: 37.775, Longitude: -122.418}
	candidate := kernel.Candidate{ID: "driver-1", Location: origin}
	if err := index.Upsert(candidate); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}

	index, err = OpenBoltIndex(path, 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	results, err := index.Search(context.Background(), origin, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != candidate {
		t.Fatalf("restored candidates = %+v, want %+v", results, candidate)
	}

	moved := kernel.Candidate{ID: "driver-1", Location: kernel.Location{Latitude: 40.7128, Longitude: -74.006}}
	if err := index.Upsert(moved); err != nil {
		t.Fatal(err)
	}
	results, err = index.Search(context.Background(), origin, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("search after moving candidate returned %+v, want none", results)
	}
	if deleted, err := index.Delete(moved.ID); err != nil || !deleted {
		t.Fatalf("delete result = %v, error = %v", deleted, err)
	}
	if deleted, err := index.Delete(moved.ID); err != nil || deleted {
		t.Fatal("delete must report true once, then false")
	}
}

func TestBoltIndexRejectsInvalidCandidate(t *testing.T) {
	index, err := OpenBoltIndex(filepath.Join(t.TempDir(), "candidates.db"), 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Upsert(kernel.Candidate{ID: "bad", Location: kernel.Location{Latitude: 91}}); err == nil {
		t.Fatal("expected invalid candidate error")
	}
}
