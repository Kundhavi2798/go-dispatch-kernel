package kernel_test

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/spatial"
)

type stressRouter struct{}

func (stressRouter) Routes(_ context.Context, _ kernel.Location, destinations []kernel.Location) ([]kernel.Route, error) {
	routes := make([]kernel.Route, len(destinations))
	for i := range routes {
		routes[i] = kernel.Route{Reachable: true, DistanceMeters: 100, DurationSeconds: 10}
	}
	return routes, nil
}

func TestDispatchStress(t *testing.T) {
	value := os.Getenv("DISPATCH_STRESS_CONCURRENCY")
	if value == "" {
		t.Skip("set DISPATCH_STRESS_CONCURRENCY to run the opt-in concurrency stress test")
	}
	concurrency, err := strconv.Atoi(value)
	if err != nil || concurrency < 1 {
		t.Fatalf("DISPATCH_STRESS_CONCURRENCY must be a positive integer, got %q", value)
	}

	index, err := spatial.NewH3Index(9, 2)
	if err != nil {
		t.Fatal(err)
	}
	origin := kernel.Location{Latitude: 37.775, Longitude: -122.418}
	if err := index.Upsert(kernel.Candidate{ID: "stress-candidate", Location: origin}); err != nil {
		t.Fatal(err)
	}
	engine, err := kernel.NewEngine(index, stressRouter{}, kernel.DurationScorer{})
	if err != nil {
		t.Fatal(err)
	}
	request := kernel.DispatchRequest{ID: "stress", Pickup: origin, MaxCandidates: 10}

	start := make(chan struct{})
	var wait sync.WaitGroup
	var failures atomic.Int64
	wait.Add(concurrency)
	begin := time.Now()
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wait.Done()
			<-start
			response, err := engine.Dispatch(context.Background(), request)
			if err != nil || len(response.Candidates) != 1 {
				failures.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	t.Logf("completed %d simultaneous dispatches in %s; failures=%d", concurrency, time.Since(begin), failures.Load())
	if failures.Load() != 0 {
		t.Fatalf("%d of %d dispatches failed", failures.Load(), concurrency)
	}
}
