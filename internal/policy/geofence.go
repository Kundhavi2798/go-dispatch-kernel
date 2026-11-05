package policy

import (
	"context"
	"fmt"
	"math"

	"dispatchkernel/internal/kernel"
)

type Geofence struct {
	vertices []kernel.Location
}

func NewGeofence(vertices []kernel.Location) (*Geofence, error) {
	if len(vertices) < 3 {
		return nil, fmt.Errorf("geofence requires at least three vertices")
	}
	copyOfVertices := append([]kernel.Location(nil), vertices...)
	for i, vertex := range copyOfVertices {
		if err := vertex.Validate(); err != nil {
			return nil, fmt.Errorf("invalid geofence vertex %d: %w", i, err)
		}
	}
	return &Geofence{vertices: copyOfVertices}, nil
}

func (geofence *Geofence) Match(_ context.Context, request kernel.DispatchRequest, candidate kernel.Candidate) (bool, error) {
	return geofence.contains(request.Pickup) && geofence.contains(candidate.Location), nil
}

func (geofence *Geofence) contains(point kernel.Location) bool {
	inside := false
	for i, j := 0, len(geofence.vertices)-1; i < len(geofence.vertices); j, i = i, i+1 {
		a, b := geofence.vertices[j], geofence.vertices[i]
		if onSegment(point, a, b) {
			return true
		}
		crosses := (a.Latitude > point.Latitude) != (b.Latitude > point.Latitude)
		if crosses {
			intersectionLongitude := (b.Longitude-a.Longitude)*(point.Latitude-a.Latitude)/(b.Latitude-a.Latitude) + a.Longitude
			if point.Longitude < intersectionLongitude {
				inside = !inside
			}
		}
	}
	return inside
}

func onSegment(point, a, b kernel.Location) bool {
	const epsilon = 1e-10
	cross := (point.Longitude-a.Longitude)*(b.Latitude-a.Latitude) -
		(point.Latitude-a.Latitude)*(b.Longitude-a.Longitude)
	if math.Abs(cross) > epsilon {
		return false
	}
	return point.Longitude >= math.Min(a.Longitude, b.Longitude)-epsilon &&
		point.Longitude <= math.Max(a.Longitude, b.Longitude)+epsilon &&
		point.Latitude >= math.Min(a.Latitude, b.Latitude)-epsilon &&
		point.Latitude <= math.Max(a.Latitude, b.Latitude)+epsilon
}
