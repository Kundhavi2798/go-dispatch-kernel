package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	dispatchv1 "dispatchkernel/gen/dispatch/v1"
	"dispatchkernel/internal/candidates"
	"dispatchkernel/internal/grpcapi"
	"dispatchkernel/internal/httpapi"
	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/policy"
	"dispatchkernel/internal/routing"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	resolution, err := envInt("H3_RESOLUTION", 9)
	if err != nil {
		return err
	}
	radius, err := envInt("H3_RADIUS_CELLS", 2)
	if err != nil {
		return err
	}
	databasePath := envString("CANDIDATE_DB_PATH", "candidates.db")
	if err := candidates.EnsureParentDirectory(databasePath); err != nil {
		return err
	}
	index, err := candidates.OpenBoltIndex(databasePath, resolution, radius)
	if err != nil {
		return err
	}
	defer func() {
		if err := index.Close(); runErr == nil && err != nil {
			runErr = err
		}
	}()
	router, err := routing.NewValhallaRouter(os.Getenv("VALHALLA_URL"), envString("VALHALLA_COSTING", "auto"), nil)
	if err != nil {
		return err
	}
	scorer, err := configuredScorer()
	if err != nil {
		return err
	}
	matchers, err := configuredMatchers()
	if err != nil {
		return err
	}
	engine, err := kernel.NewEngineWithMatchers(index, router, scorer, matchers...)
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(index, engine)
	if err != nil {
		return err
	}
	grpcHandler, err := grpcapi.NewServer(index, engine)
	if err != nil {
		return err
	}
	grpcListener, err := net.Listen("tcp", envString("GRPC_ADDR", "127.0.0.1:9090"))
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	grpcServer := grpc.NewServer()
	dispatchv1.RegisterDispatchServiceServer(grpcServer, grpcHandler)
	var profilingServer *http.Server
	if address := strings.TrimSpace(os.Getenv("PPROF_ADDR")); address != "" {
		if err := validateLoopbackAddress(address); err != nil {
			return err
		}
		profilingMux := http.NewServeMux()
		profilingMux.HandleFunc("GET /debug/pprof/", pprof.Index)
		profilingMux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		profilingMux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		profilingMux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		profilingMux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
		profilingServer = &http.Server{
			Addr:              address,
			Handler:           profilingMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}

	server := &http.Server{
		Addr:              envString("HTTP_ADDR", ":8080"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverCount := 2
	if profilingServer != nil {
		serverCount++
	}
	serverErrors := make(chan error, serverCount)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	go func() {
		serverErrors <- grpcServer.Serve(grpcListener)
	}()
	if profilingServer != nil {
		go func() {
			serverErrors <- profilingServer.ListenAndServe()
		}()
	}

	stop, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	select {
	case err := <-serverErrors:
		grpcServer.Stop()
		_ = server.Close()
		if profilingServer != nil {
			_ = profilingServer.Close()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve HTTP, gRPC, or pprof: %w", err)
		}
		return fmt.Errorf("HTTP, gRPC, or pprof server stopped unexpectedly")
	case <-stop.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		grpcServer.Stop()
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	if profilingServer != nil {
		if err := profilingServer.Shutdown(shutdownContext); err != nil {
			grpcServer.Stop()
			return fmt.Errorf("shut down pprof server: %w", err)
		}
	}
	gracefulStopDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(gracefulStopDone)
	}()
	select {
	case <-gracefulStopDone:
	case <-shutdownContext.Done():
		grpcServer.Stop()
		return fmt.Errorf("shut down gRPC server: %w", shutdownContext.Err())
	}
	for i := 0; i < serverCount; i++ {
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve HTTP, gRPC, or pprof: %w", err)
		}
	}
	return nil
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envFloat(key string, fallback float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return parsed, nil
}

func configuredScorer() (kernel.Scorer, error) {
	switch mode := envString("SCORING_MODE", "duration"); mode {
	case "duration":
		return kernel.DurationScorer{}, nil
	case "fare":
		baseFare, err := envFloat("FARE_BASE", 0)
		if err != nil {
			return nil, err
		}
		serviceFee, err := envFloat("FARE_SERVICE_FEE", 0)
		if err != nil {
			return nil, err
		}
		perKilometer, err := envFloat("FARE_PER_KILOMETER", 0)
		if err != nil {
			return nil, err
		}
		perMinute, err := envFloat("FARE_PER_MINUTE", 0)
		if err != nil {
			return nil, err
		}
		scorer, err := policy.NewFareScorer(baseFare, serviceFee, perKilometer, perMinute)
		if err != nil {
			return nil, err
		}
		return scorer, nil
	default:
		return nil, fmt.Errorf("SCORING_MODE must be duration or fare")
	}
}

func configuredMatchers() ([]kernel.Matcher, error) {
	value := strings.TrimSpace(os.Getenv("GEOFENCE_POLYGON"))
	if value == "" {
		return nil, nil
	}
	var vertices []kernel.Location
	if err := json.Unmarshal([]byte(value), &vertices); err != nil {
		return nil, fmt.Errorf("GEOFENCE_POLYGON must be a JSON array of latitude/longitude points: %w", err)
	}
	geofence, err := policy.NewGeofence(vertices)
	if err != nil {
		return nil, err
	}
	return []kernel.Matcher{geofence}, nil
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("PPROF_ADDR must be a loopback host and port: %w", err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("PPROF_ADDR must bind to localhost or a loopback IP")
	}
	return nil
}
