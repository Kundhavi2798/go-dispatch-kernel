package kernel

import (
	"context"
	"errors"
	"fmt"
	"math"
)

var (
	ErrInvalidRequest   = errors.New("invalid dispatch request")
	ErrInvalidCandidate = errors.New("invalid candidate")
)

const (
	DefaultCandidateLimit = 100
	MaxCandidateLimit     = 500
)

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (l Location) Validate() error {
	if math.IsNaN(l.Latitude) || math.IsInf(l.Latitude, 0) || l.Latitude < -90 || l.Latitude > 90 {
		return fmt.Errorf("latitude must be between -90 and 90")
	}
	if math.IsNaN(l.Longitude) || math.IsInf(l.Longitude, 0) || l.Longitude < -180 || l.Longitude > 180 {
		return fmt.Errorf("longitude must be between -180 and 180")
	}
	return nil
}

type Candidate struct {
	ID       string   `json:"id"`
	Location Location `json:"location"`
}

func (c Candidate) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidCandidate)
	}
	if err := c.Location.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCandidate, err)
	}
	return nil
}

type DispatchRequest struct {
	ID            string   `json:"id"`
	Pickup        Location `json:"pickup"`
	MaxCandidates int      `json:"max_candidates"`
}

func (r DispatchRequest) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidRequest)
	}
	if err := r.Pickup.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if r.MaxCandidates < 1 {
		return fmt.Errorf("%w: max_candidates must be positive", ErrInvalidRequest)
	}
	return nil
}

type Route struct {
	Reachable       bool    `json:"reachable"`
	DistanceMeters  float64 `json:"distance_meters"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type RankedCandidate struct {
	Candidate Candidate `json:"candidate"`
	Route     Route     `json:"route"`
	Cost      float64   `json:"cost"`
}

type DispatchResponse struct {
	RequestID  string            `json:"request_id"`
	Candidates []RankedCandidate `json:"candidates"`
}

type CandidateSource interface {
	Search(ctx context.Context, around Location, limit int) ([]Candidate, error)
}

// Router returns one route per destination, in the same order as destinations.
type Router interface {
	Routes(ctx context.Context, origin Location, destinations []Location) ([]Route, error)
}

// Scorer returns a comparable cost; lower-cost candidates are ranked first.
type Scorer interface {
	Cost(ctx context.Context, request DispatchRequest, candidate Candidate, route Route) (float64, error)
}

type Matcher interface {
	Match(ctx context.Context, request DispatchRequest, candidate Candidate) (bool, error)
}
