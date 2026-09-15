// Package health exposes safe liveness and dependency-readiness handlers.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Dependency identifies one external dependency without exposing its address.
type Dependency string

const (
	MongoDB Dependency = "mongodb"
	NATS    Dependency = "nats"
	Dex     Dependency = "dex"
)

// Checker verifies that a dependency can currently serve this process.
type Checker interface {
	Check(context.Context) error
}

// CheckFunc adapts a function to Checker.
type CheckFunc func(context.Context) error

func (f CheckFunc) Check(ctx context.Context) error { return f(ctx) }

// Pending returns a checker for a dependency whose concrete adapter has not
// yet been initialized. The internal reason is deliberately not serialized.
func Pending() Checker {
	return CheckFunc(func(context.Context) error { return errors.New("dependency adapter not initialized") })
}

// Handler provides liveness and readiness HTTP handlers.
type Handler struct {
	checks  map[Dependency]Checker
	timeout time.Duration
}

// NewHandler creates health handlers from named dependency checks.
func NewHandler(timeout time.Duration, checks map[Dependency]Checker) *Handler {
	copyOfChecks := make(map[Dependency]Checker, len(checks))
	for dependency, check := range checks {
		copyOfChecks[dependency] = check
	}
	return &Handler{checks: copyOfChecks, timeout: timeout}
}

// Routes registers health endpoints on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.liveness)
	mux.HandleFunc("GET /readyz", h.readiness)
}

func (h *Handler) liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func (h *Handler) readiness(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), h.timeout)
	defer cancel()

	checks := make(map[Dependency]checkResponse, len(h.checks))
	ready := true
	for dependency, check := range h.checks {
		status := "ready"
		if err := check.Check(ctx); err != nil {
			status = "not_ready"
			ready = false
		}
		checks[dependency] = checkResponse{Status: status}
	}

	statusCode := http.StatusOK
	status := "ready"
	if !ready {
		statusCode = http.StatusServiceUnavailable
		status = "not_ready"
	}
	writeJSON(w, statusCode, response{Status: status, Checks: checks})
}

type response struct {
	Status string                       `json:"status"`
	Checks map[Dependency]checkResponse `json:"checks,omitempty"`
}

type checkResponse struct {
	Status string `json:"status"`
}

func writeJSON(w http.ResponseWriter, status int, payload response) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
