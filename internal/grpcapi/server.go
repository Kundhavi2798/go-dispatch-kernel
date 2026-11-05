package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	dispatchv1 "dispatchkernel/gen/dispatch/v1"
	"dispatchkernel/internal/kernel"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CandidateIndex interface {
	Upsert(candidate kernel.Candidate) error
	Delete(id string) (bool, error)
}

type Dispatcher interface {
	Dispatch(ctx context.Context, request kernel.DispatchRequest) (kernel.DispatchResponse, error)
}

type Server struct {
	dispatchv1.UnimplementedDispatchServiceServer
	index      CandidateIndex
	dispatcher Dispatcher
}

func NewServer(index CandidateIndex, dispatcher Dispatcher) (*Server, error) {
	if index == nil {
		return nil, fmt.Errorf("candidate index is required")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("dispatcher is required")
	}
	return &Server{index: index, dispatcher: dispatcher}, nil
}

func (server *Server) UpsertCandidate(ctx context.Context, request *dispatchv1.UpsertCandidateRequest) (*dispatchv1.UpsertCandidateResponse, error) {
	if request == nil || request.GetCandidate() == nil || request.GetCandidate().GetLocation() == nil {
		return nil, status.Error(codes.InvalidArgument, "candidate and location are required")
	}
	candidate := kernel.Candidate{
		ID: request.GetCandidate().GetId(),
		Location: kernel.Location{
			Latitude:  request.GetCandidate().GetLocation().GetLatitude(),
			Longitude: request.GetCandidate().GetLocation().GetLongitude(),
		},
	}
	if err := server.index.Upsert(candidate); err != nil {
		if errors.Is(err, kernel.ErrInvalidCandidate) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		slog.ErrorContext(ctx, "gRPC candidate registration failed", "candidate_id", candidate.ID, "error", err)
		return nil, status.Error(codes.Internal, "candidate registration failed")
	}
	return &dispatchv1.UpsertCandidateResponse{}, nil
}

func (server *Server) DeleteCandidate(ctx context.Context, request *dispatchv1.DeleteCandidateRequest) (*dispatchv1.DeleteCandidateResponse, error) {
	if request == nil || request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "candidate id is required")
	}
	deleted, err := server.index.Delete(request.GetId())
	if err != nil {
		slog.ErrorContext(ctx, "gRPC candidate deletion failed", "candidate_id", request.GetId(), "error", err)
		return nil, status.Error(codes.Internal, "candidate deletion failed")
	}
	if !deleted {
		return nil, status.Error(codes.NotFound, "candidate not found")
	}
	return &dispatchv1.DeleteCandidateResponse{Deleted: true}, nil
}

func (server *Server) Dispatch(ctx context.Context, request *dispatchv1.DispatchRequest) (*dispatchv1.DispatchResponse, error) {
	if request == nil || request.GetPickup() == nil {
		return nil, status.Error(codes.InvalidArgument, "dispatch request and pickup are required")
	}
	maxCandidates := int(request.GetMaxCandidates())
	if maxCandidates == 0 {
		maxCandidates = kernel.DefaultCandidateLimit
	}
	if maxCandidates < 1 || maxCandidates > kernel.MaxCandidateLimit {
		return nil, status.Errorf(codes.InvalidArgument, "max_candidates must be between 1 and %d", kernel.MaxCandidateLimit)
	}
	result, err := server.dispatcher.Dispatch(ctx, kernel.DispatchRequest{
		ID:            request.GetId(),
		Pickup:        kernel.Location{Latitude: request.GetPickup().GetLatitude(), Longitude: request.GetPickup().GetLongitude()},
		MaxCandidates: maxCandidates,
	})
	if err != nil {
		if errors.Is(err, kernel.ErrInvalidRequest) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		slog.ErrorContext(ctx, "gRPC dispatch request failed", "request_id", request.GetId(), "error", err)
		return nil, status.Error(codes.Unavailable, "dispatch unavailable")
	}
	response := &dispatchv1.DispatchResponse{
		RequestId:  result.RequestID,
		Candidates: make([]*dispatchv1.RankedCandidate, 0, len(result.Candidates)),
	}
	for _, ranked := range result.Candidates {
		response.Candidates = append(response.Candidates, &dispatchv1.RankedCandidate{
			Candidate: &dispatchv1.Candidate{
				Id: ranked.Candidate.ID,
				Location: &dispatchv1.Location{
					Latitude:  ranked.Candidate.Location.Latitude,
					Longitude: ranked.Candidate.Location.Longitude,
				},
			},
			Route: &dispatchv1.Route{
				Reachable:       ranked.Route.Reachable,
				DistanceMeters:  ranked.Route.DistanceMeters,
				DurationSeconds: ranked.Route.DurationSeconds,
			},
			Cost: ranked.Cost,
		})
	}
	return response, nil
}
