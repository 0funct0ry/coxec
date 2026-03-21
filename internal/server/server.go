package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// ServerStatus represents the current lifecycle state of the server.
type ServerStatus string

const (
	StatusStarting     ServerStatus = "starting"
	StatusReady        ServerStatus = "ready"
	StatusShuttingDown ServerStatus = "shutting_down"
)

// Server represents the coxec HTTP server.
type Server struct {
	Addr       string
	Port       int
	Version    string
	StartTime  time.Time
	Status     ServerStatus
	ActiveJobs atomic.Int32
}

// NewServer creates a new Server instance.
func NewServer(addr string, port int, version string) *Server {
	return &Server{
		Addr:      addr,
		Port:      port,
		Version:   version,
		StartTime: time.Now(),
		Status:    StatusStarting,
	}
}

// Start starts the HTTP server and waits for it to shut down.
func (s *Server) Start(ctx context.Context) error {
	fullAddr := fmt.Sprintf("%s:%d", s.Addr, s.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.healthHandler)

	srv := &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	errChan := make(chan error, 1)
	go func() {
		fmt.Printf("coxec server listening on %s\n", fullAddr)
		s.Status = StatusReady
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down server...")
		s.Status = StatusShuttingDown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return fmt.Errorf("server failed: %w", err)
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	if s.Status != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": string(s.Status),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"version":        s.Version,
		"active_jobs":    s.ActiveJobs.Load(),
		"uptime_seconds": int64(time.Since(s.StartTime).Seconds()),
	})
}
