package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dispatchkernel/internal/kernel"
)

type ValhallaRouter struct {
	baseURL string
	costing string
	client  *http.Client
}

func NewValhallaRouter(baseURL, costing string, client *http.Client) (*ValhallaRouter, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("Valhalla base URL is required")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return nil, fmt.Errorf("Valhalla base URL must be an absolute HTTP or HTTPS URL")
	}
	if costing == "" {
		costing = "auto"
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &ValhallaRouter{baseURL: baseURL, costing: costing, client: client}, nil
}

func (router *ValhallaRouter) Routes(ctx context.Context, origin kernel.Location, destinations []kernel.Location) ([]kernel.Route, error) {
	if err := origin.Validate(); err != nil {
		return nil, fmt.Errorf("invalid route origin: %w", err)
	}
	if len(destinations) == 0 {
		return []kernel.Route{}, nil
	}
	requestBody := matrixRequest{
		Sources: []valhallaLocation{toValhallaLocation(origin)},
		Costing: router.costing,
		Targets: make([]valhallaLocation, len(destinations)),
	}
	for i, destination := range destinations {
		if err := destination.Validate(); err != nil {
			return nil, fmt.Errorf("invalid route destination %d: %w", i, err)
		}
		requestBody.Targets[i] = toValhallaLocation(destination)
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode Valhalla matrix request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, router.baseURL+"/sources_to_targets", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create Valhalla matrix request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := router.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Valhalla matrix: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return nil, fmt.Errorf("Valhalla matrix returned %s (read error: %w)", response.Status, readErr)
		}
		return nil, fmt.Errorf("Valhalla matrix returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxValhallaResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Valhalla matrix response: %w", err)
	}
	if len(body) > maxValhallaResponseBytes {
		return nil, fmt.Errorf("Valhalla matrix response exceeds %d bytes", maxValhallaResponseBytes)
	}
	var result matrixResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode Valhalla matrix response: %w", err)
	}
	if len(result.SourcesToTargets) != 1 || len(result.SourcesToTargets[0]) != len(destinations) {
		return nil, fmt.Errorf("Valhalla returned an unexpected matrix shape")
	}
	routes := make([]kernel.Route, len(destinations))
	for i, cell := range result.SourcesToTargets[0] {
		if cell == nil {
			continue
		}
		if cell.Distance < 0 || cell.Time < 0 {
			return nil, fmt.Errorf("Valhalla returned negative metrics for destination %d", i)
		}
		routes[i] = kernel.Route{
			Reachable:       true,
			DistanceMeters:  cell.Distance * 1000,
			DurationSeconds: cell.Time,
		}
	}
	return routes, nil
}

const maxValhallaResponseBytes = 4 << 20

func toValhallaLocation(location kernel.Location) valhallaLocation {
	return valhallaLocation{Latitude: location.Latitude, Longitude: location.Longitude}
}

type valhallaLocation struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

type matrixRequest struct {
	Sources []valhallaLocation `json:"sources"`
	Targets []valhallaLocation `json:"targets"`
	Costing string             `json:"costing"`
}

type matrixResponse struct {
	SourcesToTargets [][]*matrixCell `json:"sources_to_targets"`
}

type matrixCell struct {
	Distance float64 `json:"distance"`
	Time     float64 `json:"time"`
}
