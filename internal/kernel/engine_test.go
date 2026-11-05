package kernel

import (
	"context"
	"errors"
	"testing"
)

type candidateSourceFunc func(context.Context, Location, int) ([]Candidate, error)

func (source candidateSourceFunc) Search(ctx context.Context, around Location, limit int) ([]Candidate, error) {
	return source(ctx, around, limit)
}

type routerFunc func(context.Context, Location, []Location) ([]Route, error)

func (router routerFunc) Routes(ctx context.Context, origin Location, destinations []Location) ([]Route, error) {
	return router(ctx, origin, destinations)
}

type matcherFunc func(context.Context, DispatchRequest, Candidate) (bool, error)

func (matcher matcherFunc) Match(ctx context.Context, request DispatchRequest, candidate Candidate) (bool, error) {
	return matcher(ctx, request, candidate)
}

func TestEngineDispatchRanksReachableCandidates(t *testing.T) {
	candidates := []Candidate{
		{ID: "slower", Location: Location{Latitude: 1, Longitude: 2}},
		{ID: "faster", Location: Location{Latitude: 3, Longitude: 4}},
		{ID: "unreachable", Location: Location{Latitude: 5, Longitude: 6}},
	}
	routerCalls := 0
	engine, err := NewEngine(
		candidateSourceFunc(func(_ context.Context, _ Location, limit int) ([]Candidate, error) {
			if limit != 3 {
				t.Fatalf("candidate limit = %d, want 3", limit)
			}
			return candidates, nil
		}),
		routerFunc(func(_ context.Context, _ Location, destinations []Location) ([]Route, error) {
			routerCalls++
			if len(destinations) != len(candidates) {
				t.Fatalf("routed %d destinations, want %d", len(destinations), len(candidates))
			}
			return []Route{
				{Reachable: true, DistanceMeters: 100, DurationSeconds: 30},
				{Reachable: true, DistanceMeters: 50, DurationSeconds: 10},
				{},
			}, nil
		}),
		DurationScorer{},
	)
	if err != nil {
		t.Fatal(err)
	}

	response, err := engine.Dispatch(context.Background(), DispatchRequest{
		ID:            "request-1",
		Pickup:        Location{Latitude: 0, Longitude: 0},
		MaxCandidates: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if routerCalls != 1 {
		t.Fatalf("router called %d times, want one batched call", routerCalls)
	}
	if response.RequestID != "request-1" || len(response.Candidates) != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Candidates[0].Candidate.ID != "faster" || response.Candidates[1].Candidate.ID != "slower" {
		t.Fatalf("unexpected candidate ranking: %+v", response.Candidates)
	}
}

func TestEngineRejectsRouterShapeMismatch(t *testing.T) {
	engine, err := NewEngine(
		candidateSourceFunc(func(context.Context, Location, int) ([]Candidate, error) {
			return []Candidate{{ID: "one", Location: Location{Latitude: 1, Longitude: 1}}}, nil
		}),
		routerFunc(func(context.Context, Location, []Location) ([]Route, error) {
			return nil, nil
		}),
		DurationScorer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Dispatch(context.Background(), DispatchRequest{
		ID: "r", Pickup: Location{}, MaxCandidates: 1,
	})
	if err == nil {
		t.Fatal("expected router shape error")
	}
}

func TestEnginePropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine, err := NewEngine(
		candidateSourceFunc(func(context.Context, Location, int) ([]Candidate, error) {
			t.Fatal("candidate source called after cancellation")
			return nil, nil
		}),
		routerFunc(func(context.Context, Location, []Location) ([]Route, error) {
			return nil, nil
		}),
		DurationScorer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Dispatch(ctx, DispatchRequest{ID: "r", Pickup: Location{}, MaxCandidates: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestEngineAppliesMatchersBeforeRouting(t *testing.T) {
	routed := 0
	engine, err := NewEngineWithMatchers(
		candidateSourceFunc(func(context.Context, Location, int) ([]Candidate, error) {
			return []Candidate{
				{ID: "inside", Location: Location{Latitude: 1, Longitude: 1}},
				{ID: "outside", Location: Location{Latitude: 2, Longitude: 2}},
			}, nil
		}),
		routerFunc(func(_ context.Context, _ Location, destinations []Location) ([]Route, error) {
			routed = len(destinations)
			return []Route{{Reachable: true, DurationSeconds: 1}}, nil
		}),
		DurationScorer{},
		matcherFunc(func(_ context.Context, _ DispatchRequest, candidate Candidate) (bool, error) {
			return candidate.ID == "inside", nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := engine.Dispatch(context.Background(), DispatchRequest{ID: "r", Pickup: Location{}, MaxCandidates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if routed != 1 || len(response.Candidates) != 1 || response.Candidates[0].Candidate.ID != "inside" {
		t.Fatalf("matcher was not applied before routing: routed=%d response=%+v", routed, response)
	}
}
