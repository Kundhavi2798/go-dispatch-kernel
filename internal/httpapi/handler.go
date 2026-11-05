package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"dispatchkernel/internal/kernel"
)

const maxRequestBytes = 1 << 20

type CandidateIndex interface {
	Upsert(candidate kernel.Candidate) error
	Delete(id string) (bool, error)
}

type Dispatcher interface {
	Dispatch(ctx context.Context, request kernel.DispatchRequest) (kernel.DispatchResponse, error)
}

type Handler struct {
	index      CandidateIndex
	dispatcher Dispatcher
}

func NewHandler(index CandidateIndex, dispatcher Dispatcher) (*Handler, error) {
	if index == nil {
		return nil, fmt.Errorf("candidate index is required")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("dispatcher is required")
	}
	return &Handler{index: index, dispatcher: dispatcher}, nil
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("PUT /v1/candidates/{id}", handler.upsertCandidate)
	mux.HandleFunc("DELETE /v1/candidates/{id}", handler.deleteCandidate)
	mux.HandleFunc("POST /v1/dispatch", handler.dispatch)
	return mux
}

func (handler *Handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) upsertCandidate(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Location kernel.Location `json:"location"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	candidate := kernel.Candidate{ID: request.PathValue("id"), Location: body.Location}
	if err := handler.index.Upsert(candidate); err != nil {
		if errors.Is(err, kernel.ErrInvalidCandidate) {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		slog.ErrorContext(request.Context(), "upsert candidate failed", "candidate_id", candidate.ID, "error", err)
		writeError(writer, http.StatusInternalServerError, "candidate registration failed")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) deleteCandidate(writer http.ResponseWriter, request *http.Request) {
	deleted, err := handler.index.Delete(request.PathValue("id"))
	if err != nil {
		slog.ErrorContext(request.Context(), "delete candidate failed", "candidate_id", request.PathValue("id"), "error", err)
		writeError(writer, http.StatusInternalServerError, "candidate deletion failed")
		return
	}
	if !deleted {
		writeError(writer, http.StatusNotFound, "candidate not found")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) dispatch(writer http.ResponseWriter, request *http.Request) {
	var input kernel.DispatchRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if input.MaxCandidates == 0 {
		input.MaxCandidates = kernel.DefaultCandidateLimit
	}
	if input.MaxCandidates < 1 || input.MaxCandidates > kernel.MaxCandidateLimit {
		writeError(writer, http.StatusBadRequest, "max_candidates must be between 1 and 500")
		return
	}
	response, err := handler.dispatcher.Dispatch(request.Context(), input)
	if err != nil {
		if errors.Is(err, kernel.ErrInvalidRequest) {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		slog.ErrorContext(request.Context(), "dispatch request failed", "request_id", input.ID, "error", err)
		writeError(writer, http.StatusServiceUnavailable, "dispatch unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
