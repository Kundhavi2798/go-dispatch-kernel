package spatial

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/ziprecruiter/h3-go/pkg/h3"

	"dispatchkernel/internal/kernel"
)

type H3Index struct {
	resolution int
	radius     int
	mu         sync.RWMutex
	cells      map[h3.Cell]map[string]kernel.Candidate
	byID       map[string]h3.Cell
}

type candidateDistance struct {
	candidate kernel.Candidate
	distance  float64
}

func NewH3Index(resolution, radius int) (*H3Index, error) {
	if resolution < 0 || resolution > 15 {
		return nil, fmt.Errorf("H3 resolution must be between 0 and 15")
	}
	if radius < 0 || radius > 20 {
		return nil, fmt.Errorf("H3 search radius must be between 0 and 20")
	}
	return &H3Index{
		resolution: resolution,
		radius:     radius,
		cells:      make(map[h3.Cell]map[string]kernel.Candidate),
		byID:       make(map[string]h3.Cell),
	}, nil
}

func (index *H3Index) Upsert(candidate kernel.Candidate) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	cell, err := h3.NewCellFromLatLng(h3.NewLatLng(candidate.Location.Latitude, candidate.Location.Longitude), index.resolution)
	if err != nil {
		return fmt.Errorf("index candidate %q: %w", candidate.ID, err)
	}

	index.mu.Lock()
	defer index.mu.Unlock()
	if oldCell, exists := index.byID[candidate.ID]; exists && oldCell != cell {
		delete(index.cells[oldCell], candidate.ID)
		if len(index.cells[oldCell]) == 0 {
			delete(index.cells, oldCell)
		}
	}
	if index.cells[cell] == nil {
		index.cells[cell] = make(map[string]kernel.Candidate)
	}
	index.cells[cell][candidate.ID] = candidate
	index.byID[candidate.ID] = cell
	return nil
}

func (index *H3Index) Delete(id string) bool {
	index.mu.Lock()
	defer index.mu.Unlock()
	cell, exists := index.byID[id]
	if !exists {
		return false
	}
	delete(index.byID, id)
	delete(index.cells[cell], id)
	if len(index.cells[cell]) == 0 {
		delete(index.cells, cell)
	}
	return true
}

func (index *H3Index) Search(ctx context.Context, around kernel.Location, limit int) ([]kernel.Candidate, error) {
	if err := around.Validate(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, fmt.Errorf("candidate limit must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cell, err := h3.NewCellFromLatLng(h3.NewLatLng(around.Latitude, around.Longitude), index.resolution)
	if err != nil {
		return nil, fmt.Errorf("index search origin: %w", err)
	}
	neighbors, err := cell.GridDisk(index.radius)
	if err != nil {
		return nil, fmt.Errorf("find H3 neighbors: %w", err)
	}

	initialCapacity := limit
	if initialCapacity > 64 {
		initialCapacity = 64
	}
	matches := make([]candidateDistance, 0, initialCapacity)
	index.mu.RLock()
	for _, neighbor := range neighbors {
		if err := ctx.Err(); err != nil {
			index.mu.RUnlock()
			return nil, err
		}
		for _, candidate := range index.cells[neighbor] {
			if err := ctx.Err(); err != nil {
				index.mu.RUnlock()
				return nil, err
			}
			match := candidateDistance{
				candidate: candidate,
				distance:  haversineMeters(around, candidate.Location),
			}
			if len(matches) < limit {
				matches = append(matches, match)
				siftUpMax(matches, len(matches)-1)
			} else if candidateDistanceLess(match, matches[0]) {
				matches[0] = match
				siftDownMax(matches, 0)
			}
		}
	}
	index.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool {
		return candidateDistanceLess(matches[i], matches[j])
	})
	candidates := make([]kernel.Candidate, len(matches))
	for i := range matches {
		candidates[i] = matches[i].candidate
	}
	return candidates, nil
}

func candidateDistanceLess(left, right candidateDistance) bool {
	if left.distance == right.distance {
		return left.candidate.ID < right.candidate.ID
	}
	return left.distance < right.distance
}

func candidateDistanceGreater(left, right candidateDistance) bool {
	return candidateDistanceLess(right, left)
}

func siftUpMax(values []candidateDistance, index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !candidateDistanceGreater(values[index], values[parent]) {
			return
		}
		values[index], values[parent] = values[parent], values[index]
		index = parent
	}
}

func siftDownMax(values []candidateDistance, index int) {
	for {
		largest := index
		left, right := index*2+1, index*2+2
		if left < len(values) && candidateDistanceGreater(values[left], values[largest]) {
			largest = left
		}
		if right < len(values) && candidateDistanceGreater(values[right], values[largest]) {
			largest = right
		}
		if largest == index {
			return
		}
		values[index], values[largest] = values[largest], values[index]
		index = largest
	}
}

func haversineMeters(a, b kernel.Location) float64 {
	const earthRadiusMeters = 6371008.8
	latitudeA, latitudeB := a.Latitude*math.Pi/180, b.Latitude*math.Pi/180
	deltaLatitude := latitudeB - latitudeA
	deltaLongitude := (b.Longitude - a.Longitude) * math.Pi / 180
	sinLatitude := math.Sin(deltaLatitude / 2)
	sinLongitude := math.Sin(deltaLongitude / 2)
	value := sinLatitude*sinLatitude + math.Cos(latitudeA)*math.Cos(latitudeB)*sinLongitude*sinLongitude
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(math.Min(1, value)))
}
