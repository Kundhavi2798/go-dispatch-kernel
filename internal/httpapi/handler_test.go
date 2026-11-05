package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/spatial"
)

type testRouter struct{}

func (testRouter) Routes(_ context.Context, _ kernel.Location, destinations []kernel.Location) ([]kernel.Route, error) {
	routes := make([]kernel.Route, len(destinations))
	for i := range routes {
		routes[i] = kernel.Route{Reachable: true, DurationSeconds: 12}
	}
	return routes, nil
}

type testIndex struct {
	index *spatial.H3Index
}

func (index *testIndex) Upsert(candidate kernel.Candidate) error {
	return index.index.Upsert(candidate)
}

func (index *testIndex) Search(ctx context.Context, around kernel.Location, limit int) ([]kernel.Candidate, error) {
	return index.index.Search(ctx, around, limit)
}

func (index *testIndex) Delete(id string) (bool, error) {
	return index.index.Delete(id), nil
}

func TestCandidateRegistrationAndDispatch(t *testing.T) {
	spatialIndex, err := spatial.NewH3Index(9, 2)
	if err != nil {
		t.Fatal(err)
	}
	index := &testIndex{index: spatialIndex}
	engine, err := kernel.NewEngine(index, testRouter{}, kernel.DurationScorer{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(index, engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler.Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodPut, server.URL+"/v1/candidates/c-1", strings.NewReader(
		`{"location":{"latitude":37.775,"longitude":-122.418}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("candidate registration status = %d, want 204", response.StatusCode)
	}

	response, err = server.Client().Post(
		server.URL+"/v1/dispatch",
		"application/json",
		strings.NewReader(`{"id":"r-1","pickup":{"latitude":37.775,"longitude":-122.418}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status = %d, want 200", response.StatusCode)
	}
	var result kernel.DispatchResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "r-1" || len(result.Candidates) != 1 || result.Candidates[0].Candidate.ID != "c-1" {
		t.Fatalf("unexpected dispatch response: %+v", result)
	}
}

func TestDispatchRejectsUnknownFields(t *testing.T) {
	spatialIndex, err := spatial.NewH3Index(9, 2)
	if err != nil {
		t.Fatal(err)
	}
	index := &testIndex{index: spatialIndex}
	engine, err := kernel.NewEngine(index, testRouter{}, kernel.DurationScorer{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(index, engine)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/dispatch", strings.NewReader(
		`{"id":"r-1","pickup":{"latitude":0,"longitude":0},"unexpected":true}`,
	))
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", recorder.Code, recorder.Body.String())
	}
}
