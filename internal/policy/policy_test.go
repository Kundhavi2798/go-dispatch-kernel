package policy

import (
	"context"
	"testing"

	"dispatchkernel/internal/kernel"
)

func TestGeofenceMatchesPickupAndCandidate(t *testing.T) {
	geofence, err := NewGeofence([]kernel.Location{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 10},
		{Latitude: 10, Longitude: 10},
		{Latitude: 10, Longitude: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := kernel.DispatchRequest{Pickup: kernel.Location{Latitude: 5, Longitude: 5}}
	inside := kernel.Candidate{ID: "inside", Location: kernel.Location{Latitude: 2, Longitude: 2}}
	matches, err := geofence.Match(context.Background(), request, inside)
	if err != nil || !matches {
		t.Fatalf("inside candidate match = %v, error = %v", matches, err)
	}
	outside := kernel.Candidate{ID: "outside", Location: kernel.Location{Latitude: 12, Longitude: 2}}
	matches, err = geofence.Match(context.Background(), request, outside)
	if err != nil || matches {
		t.Fatalf("outside candidate match = %v, error = %v", matches, err)
	}
}

func TestFareScorerCalculatesConfiguredCost(t *testing.T) {
	scorer, err := NewFareScorer(2, 1, 1.5, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := scorer.Cost(context.Background(), kernel.DispatchRequest{}, kernel.Candidate{}, kernel.Route{
		DistanceMeters: 2000, DurationSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cost != 9 {
		t.Fatalf("fare = %v, want 9", cost)
	}
}

func TestFareScorerRejectsInvalidRates(t *testing.T) {
	if _, err := NewFareScorer(1, 0, -1, 0); err == nil {
		t.Fatal("expected invalid fare rate error")
	}
}
