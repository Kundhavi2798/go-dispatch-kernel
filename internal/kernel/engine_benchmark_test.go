package kernel_test

import (
	"context"
	"fmt"
	"testing"

	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/spatial"
)

type benchmarkRouter struct{}

func (benchmarkRouter) Routes(_ context.Context, _ kernel.Location, destinations []kernel.Location) ([]kernel.Route, error) {
	routes := make([]kernel.Route, len(destinations))
	for i := range routes {
		routes[i] = kernel.Route{Reachable: true, DistanceMeters: 500, DurationSeconds: 30}
	}
	return routes, nil
}

func BenchmarkDispatchPipeline(b *testing.B) {
	index, err := spatial.NewH3Index(9, 2)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		candidate := kernel.Candidate{
			ID:       fmt.Sprintf("candidate-%d", i),
			Location: kernel.Location{Latitude: 37.775 + float64(i%20)*0.00001, Longitude: -122.418},
		}
		if err := index.Upsert(candidate); err != nil {
			b.Fatal(err)
		}
	}
	engine, err := kernel.NewEngine(index, benchmarkRouter{}, kernel.DurationScorer{})
	if err != nil {
		b.Fatal(err)
	}
	request := kernel.DispatchRequest{
		ID:            "benchmark",
		Pickup:        kernel.Location{Latitude: 37.775, Longitude: -122.418},
		MaxCandidates: 100,
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := engine.Dispatch(context.Background(), request); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
