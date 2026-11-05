package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dispatchkernel/internal/kernel"
)

func TestValhallaRouterRoutesBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sources_to_targets" {
			t.Errorf("path = %q, want /sources_to_targets", request.URL.Path)
		}
		var input matrixRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(input.Sources) != 1 || len(input.Targets) != 2 || input.Costing != "auto" {
			t.Errorf("unexpected matrix request: %+v", input)
		}
		_, _ = writer.Write([]byte(`{"sources_to_targets":[[{"distance":1.25,"time":90},null]]}`))
	}))
	defer server.Close()

	router, err := NewValhallaRouter(server.URL, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	routes, err := router.Routes(context.Background(), kernel.Location{Latitude: 1, Longitude: 2}, []kernel.Location{
		{Latitude: 3, Longitude: 4},
		{Latitude: 5, Longitude: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || !routes[0].Reachable || routes[0].DistanceMeters != 1250 || routes[0].DurationSeconds != 90 {
		t.Fatalf("unexpected reachable route: %+v", routes)
	}
	if routes[1].Reachable {
		t.Fatalf("null matrix cell should be unreachable: %+v", routes[1])
	}
}

func TestValhallaRouterReportsUpstreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "bad costing", http.StatusBadRequest)
	}))
	defer server.Close()

	router, err := NewValhallaRouter(server.URL, "invalid", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = router.Routes(context.Background(), kernel.Location{}, []kernel.Location{{}})
	if err == nil {
		t.Fatal("expected upstream status error")
	}
}
