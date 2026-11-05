package grpcapi

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	dispatchv1 "dispatchkernel/gen/dispatch/v1"
	"dispatchkernel/internal/candidates"
	"dispatchkernel/internal/kernel"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const grpcBufferSize = 1024 * 1024

type testRouter struct{}

func (testRouter) Routes(_ context.Context, _ kernel.Location, destinations []kernel.Location) ([]kernel.Route, error) {
	routes := make([]kernel.Route, len(destinations))
	for i := range routes {
		routes[i] = kernel.Route{Reachable: true, DistanceMeters: 100, DurationSeconds: 20}
	}
	return routes, nil
}

func TestGRPCDispatchService(t *testing.T) {
	index, err := candidates.OpenBoltIndex(filepath.Join(t.TempDir(), "candidates.db"), 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	engine, err := kernel.NewEngine(index, testRouter{}, kernel.DurationScorer{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServer(index, engine)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(grpcBufferSize)
	server := grpc.NewServer()
	dispatchv1.RegisterDispatchServiceServer(server, service)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := dispatchv1.NewDispatchServiceClient(connection)
	ctx := context.Background()

	_, err = client.UpsertCandidate(ctx, &dispatchv1.UpsertCandidateRequest{
		Candidate: &dispatchv1.Candidate{
			Id:       "driver-1",
			Location: &dispatchv1.Location{Latitude: 37.775, Longitude: -122.418},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Dispatch(ctx, &dispatchv1.DispatchRequest{
		Id:     "request-1",
		Pickup: &dispatchv1.Location{Latitude: 37.775, Longitude: -122.418},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetRequestId() != "request-1" || len(result.GetCandidates()) != 1 ||
		result.GetCandidates()[0].GetCandidate().GetId() != "driver-1" {
		t.Fatalf("unexpected gRPC response: %+v", result)
	}
}

func TestGRPCDispatchRejectsInvalidRequest(t *testing.T) {
	index, err := candidates.OpenBoltIndex(filepath.Join(t.TempDir(), "candidates.db"), 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	engine, err := kernel.NewEngine(index, testRouter{}, kernel.DurationScorer{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServer(index, engine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Dispatch(context.Background(), &dispatchv1.DispatchRequest{
		Id:     "request-1",
		Pickup: &dispatchv1.Location{Latitude: 91, Longitude: 0},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status = %v, want InvalidArgument", status.Code(err))
	}
}
