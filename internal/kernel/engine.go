package kernel

import (
	"context"
	"fmt"
	"math"
	"sort"
)

type Engine struct {
	candidates CandidateSource
	router     Router
	scorer     Scorer
	matchers   []Matcher
}

func NewEngine(candidates CandidateSource, router Router, scorer Scorer) (*Engine, error) {
	return NewEngineWithMatchers(candidates, router, scorer)
}

func NewEngineWithMatchers(candidates CandidateSource, router Router, scorer Scorer, matchers ...Matcher) (*Engine, error) {
	if candidates == nil {
		return nil, fmt.Errorf("candidate source is required")
	}
	if router == nil {
		return nil, fmt.Errorf("router is required")
	}
	if scorer == nil {
		return nil, fmt.Errorf("scorer is required")
	}
	for i, matcher := range matchers {
		if matcher == nil {
			return nil, fmt.Errorf("matcher %d is required", i)
		}
	}
	return &Engine{candidates: candidates, router: router, scorer: scorer, matchers: matchers}, nil
}

func (e *Engine) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResponse, error) {
	if err := request.Validate(); err != nil {
		return DispatchResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return DispatchResponse{}, err
	}

	candidates, err := e.candidates.Search(ctx, request.Pickup, request.MaxCandidates)
	if err != nil {
		return DispatchResponse{}, fmt.Errorf("search candidates: %w", err)
	}
	if len(candidates) > request.MaxCandidates {
		return DispatchResponse{}, fmt.Errorf("candidate source returned %d results for limit %d", len(candidates), request.MaxCandidates)
	}
	seen := make(map[string]struct{}, len(candidates))
	matched := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return DispatchResponse{}, err
		}
		if err := candidate.Validate(); err != nil {
			return DispatchResponse{}, fmt.Errorf("candidate source returned invalid candidate: %w", err)
		}
		if _, exists := seen[candidate.ID]; exists {
			return DispatchResponse{}, fmt.Errorf("candidate source returned duplicate candidate %q", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		include := true
		for _, matcher := range e.matchers {
			include, err = matcher.Match(ctx, request, candidate)
			if err != nil {
				return DispatchResponse{}, fmt.Errorf("match candidate %q: %w", candidate.ID, err)
			}
			if !include {
				break
			}
		}
		if include {
			matched = append(matched, candidate)
		}
	}
	candidates = matched
	response := DispatchResponse{
		RequestID:  request.ID,
		Candidates: make([]RankedCandidate, 0, len(candidates)),
	}
	if len(candidates) == 0 {
		return response, nil
	}

	destinations := make([]Location, len(candidates))
	for i := range candidates {
		destinations[i] = candidates[i].Location
	}
	routes, err := e.router.Routes(ctx, request.Pickup, destinations)
	if err != nil {
		return DispatchResponse{}, fmt.Errorf("route candidates: %w", err)
	}
	if len(routes) != len(candidates) {
		return DispatchResponse{}, fmt.Errorf("router returned %d routes for %d candidates", len(routes), len(candidates))
	}

	for i, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return DispatchResponse{}, err
		}
		route := routes[i]
		if !route.Reachable {
			continue
		}
		if !finiteNonNegative(route.DistanceMeters) || !finiteNonNegative(route.DurationSeconds) {
			return DispatchResponse{}, fmt.Errorf("router returned invalid metrics for candidate %q", candidate.ID)
		}
		cost, err := e.scorer.Cost(ctx, request, candidate, route)
		if err != nil {
			return DispatchResponse{}, fmt.Errorf("score candidate %q: %w", candidate.ID, err)
		}
		if math.IsNaN(cost) || math.IsInf(cost, 0) {
			return DispatchResponse{}, fmt.Errorf("scorer returned invalid cost for candidate %q", candidate.ID)
		}
		response.Candidates = append(response.Candidates, RankedCandidate{
			Candidate: candidate,
			Route:     route,
			Cost:      cost,
		})
	}

	sort.Slice(response.Candidates, func(i, j int) bool {
		left, right := response.Candidates[i], response.Candidates[j]
		if left.Cost == right.Cost {
			return left.Candidate.ID < right.Candidate.ID
		}
		return left.Cost < right.Cost
	})
	return response, nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

type DurationScorer struct{}

func (DurationScorer) Cost(_ context.Context, _ DispatchRequest, _ Candidate, route Route) (float64, error) {
	return route.DurationSeconds, nil
}
