package main

import (
	"testing"

	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/policy"
)

func TestConfiguredScorerDefaultsToDuration(t *testing.T) {
	t.Setenv("SCORING_MODE", "")
	scorer, err := configuredScorer()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scorer.(kernel.DurationScorer); !ok {
		t.Fatalf("default scorer type = %T, want kernel.DurationScorer", scorer)
	}
}

func TestConfiguredScorerSupportsFareMode(t *testing.T) {
	t.Setenv("SCORING_MODE", "fare")
	t.Setenv("FARE_BASE", "2")
	t.Setenv("FARE_SERVICE_FEE", "1")
	t.Setenv("FARE_PER_KILOMETER", "1.5")
	t.Setenv("FARE_PER_MINUTE", "0.6")
	scorer, err := configuredScorer()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scorer.(policy.FareScorer); !ok {
		t.Fatalf("fare scorer type = %T, want policy.FareScorer", scorer)
	}
}

func TestConfiguredMatchersParsesGeofence(t *testing.T) {
	t.Setenv("GEOFENCE_POLYGON", `[{"latitude":0,"longitude":0},{"latitude":0,"longitude":1},{"latitude":1,"longitude":1}]`)
	matchers, err := configuredMatchers()
	if err != nil {
		t.Fatal(err)
	}
	if len(matchers) != 1 {
		t.Fatalf("matcher count = %d, want 1", len(matchers))
	}
}

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:6060", "localhost:6060", "[::1]:6060"} {
		if err := validateLoopbackAddress(address); err != nil {
			t.Errorf("%q should be accepted: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:6060", ":6060", "localhost"} {
		if err := validateLoopbackAddress(address); err == nil {
			t.Errorf("%q should be rejected", address)
		}
	}
}

func TestConfiguredScorerRejectsInvalidFareRate(t *testing.T) {
	t.Setenv("SCORING_MODE", "fare")
	t.Setenv("FARE_PER_KILOMETER", "-1")
	_, err := configuredScorer()
	if err == nil {
		t.Fatal("expected invalid fare rate error")
	}
}

func TestConfiguredScorerRejectsUnknownMode(t *testing.T) {
	t.Setenv("SCORING_MODE", "unknown")
	_, err := configuredScorer()
	if err == nil {
		t.Fatal("expected unsupported scoring mode error")
	}
}

func TestConfiguredMatchersRejectsInvalidPolygon(t *testing.T) {
	t.Setenv("GEOFENCE_POLYGON", "not-json")
	if _, err := configuredMatchers(); err == nil {
		t.Fatal("expected invalid geofence error")
	}
}
