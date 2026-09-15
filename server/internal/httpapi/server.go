// Package httpapi owns the Record Hub HTTP server lifecycle.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Server is an app service backed by net/http.
type Server struct {
	server          *http.Server
	shutdownTimeout time.Duration
}

// New creates an HTTP server with conservative transport timeouts.
func New(address string, handler http.Handler, shutdownTimeout time.Duration) *Server {
	return &Server{
		server: &http.Server{
			Addr:              address,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		shutdownTimeout: shutdownTimeout,
	}
}

func (s *Server) Name() string { return "api" }

// Run serves until cancellation, then drains active requests within the
// configured shutdown timeout.
func (s *Server) Run(ctx context.Context) error {
	result := make(chan error, 1)
	go func() { result <- s.server.ListenAndServe() }()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
