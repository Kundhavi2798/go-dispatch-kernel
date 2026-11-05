package spatial

import (
	"context"
	"strconv"
	"testing"

	"dispatchkernel/internal/kernel"
)

func TestH3IndexUpsertSearchAndDelete(t *testing.T) {
	index, err := NewH3Index(9, 2)
	if err != nil {
		t.Fatal(err)
	}
	origin := kernel.Location{Latitude: 37.775, Longitude: -122.418}
	near := kernel.Candidate{ID: "near", Location: kernel.Location{Latitude: 37.7751, Longitude: -122.418}}
	far := kernel.Candidate{ID: "far", Location: kernel.Location{Latitude: 37.777, Longitude: -122.418}}
	for _, candidate := range []kernel.Candidate{near, far} {
		if err := index.Upsert(candidate); err != nil {
			t.Fatal(err)
		}
	}

	results, err := index.Search(context.Background(), origin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "near" {
		t.Fatalf("nearest result = %+v, want near candidate", results)
	}

	moved := kernel.Candidate{ID: "near", Location: kernel.Location{Latitude: 40.7128, Longitude: -74.006}}
	if err := index.Upsert(moved); err != nil {
		t.Fatal(err)
	}
	results, err = index.Search(context.Background(), origin, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "far" {
		t.Fatalf("results after moving candidate = %+v, want only far candidate", results)
	}

	if !index.Delete("far") || index.Delete("far") {
		t.Fatal("delete must report true once, then false")
	}
	results, err = index.Search(context.Background(), origin, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results after delete = %+v, want none", results)
	}
}

func TestH3IndexRejectsInvalidCoordinatesAndLimits(t *testing.T) {
	index, err := NewH3Index(9, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(kernel.Candidate{ID: "bad", Location: kernel.Location{Latitude: 91}}); err == nil {
		t.Fatal("expected invalid coordinate error")
	}
	if _, err := index.Search(context.Background(), kernel.Location{}, 0); err == nil {
		t.Fatal("expected invalid candidate limit error")
	}
}

func BenchmarkH3IndexSearch(b *testing.B) {
	index := newBenchmarkIndex(b)
	origin := kernel.Location{Latitude: 37.775, Longitude: -122.418}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := index.Search(context.Background(), origin, 100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkH3IndexSearchParallel(b *testing.B) {
	index := newBenchmarkIndex(b)
	origin := kernel.Location{Latitude: 37.775, Longitude: -122.418}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := index.Search(context.Background(), origin, 100); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func newBenchmarkIndex(b *testing.B) *H3Index {
	b.Helper()
	index, err := NewH3Index(9, 2)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		candidate := kernel.Candidate{
			ID:       strconv.Itoa(i),
			Location: kernel.Location{Latitude: 37.775 + float64(i%50)*0.00001, Longitude: -122.418},
		}
		if err := index.Upsert(candidate); err != nil {
			b.Fatal(err)
		}
	}
	return index
}
