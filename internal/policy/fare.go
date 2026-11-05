package policy

import (
	"context"
	"fmt"
	"math"

	"dispatchkernel/internal/kernel"
)

type FareScorer struct {
	BaseFare     float64
	ServiceFee   float64
	PerKilometer float64
	PerMinute    float64
}

func NewFareScorer(baseFare, serviceFee, perKilometer, perMinute float64) (FareScorer, error) {
	rates := []struct {
		name  string
		value float64
	}{
		{name: "base fare", value: baseFare},
		{name: "service fee", value: serviceFee},
		{name: "per kilometer", value: perKilometer},
		{name: "per minute", value: perMinute},
	}
	for _, rate := range rates {
		if math.IsNaN(rate.value) || math.IsInf(rate.value, 0) || rate.value < 0 {
			return FareScorer{}, fmt.Errorf("%s must be a finite non-negative value", rate.name)
		}
	}
	return FareScorer{
		BaseFare:     baseFare,
		ServiceFee:   serviceFee,
		PerKilometer: perKilometer,
		PerMinute:    perMinute,
	}, nil
}

func (scorer FareScorer) Cost(_ context.Context, _ kernel.DispatchRequest, _ kernel.Candidate, route kernel.Route) (float64, error) {
	return scorer.BaseFare + scorer.ServiceFee +
		(route.DistanceMeters/1000)*scorer.PerKilometer +
		(route.DurationSeconds/60)*scorer.PerMinute, nil
}
