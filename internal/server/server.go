package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Server represents the coxec HTTP server.
type Server struct {
	Addr string
	Port int
}

// NewServer creates a new Server instance.
func NewServer(addr string, port int) *Server {
	return &Server{
		Addr: addr,
		Port: port,
	}
}

// Start starts the HTTP server and waits for it to shut down.
func (s *Server) Start(ctx context.Context) error {
	fullAddr := fmt.Sprintf("%s:%d", s.Addr, s.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	errChan := make(chan error, 1)
	go func() {
		fmt.Printf("coxec server listening on %s\n", fullAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return fmt.Errorf("server failed: %w", err)
	}
}
