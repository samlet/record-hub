// Package app coordinates process responsibilities and graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/samlet/record-hub/server/internal/config"
	"github.com/samlet/record-hub/server/internal/health"
	"github.com/samlet/record-hub/server/internal/httpapi"
	"github.com/samlet/record-hub/server/internal/observability"
)

// Service is one long-running process responsibility.
type Service interface {
	Name() string
	Run(context.Context) error
}

// App runs all services selected for a process mode.
type App struct {
	logger          *slog.Logger
	shutdownTimeout time.Duration
	services        []Service
}

// New assembles the responsibilities selected by mode. Concrete API and
// worker services replace the idle boundaries as their modules are delivered.
func New(cfg config.Config, logger *slog.Logger) *App {
	services := make([]Service, 0, 2)
	if cfg.Mode == config.ModeAPI || cfg.Mode == config.ModeAll {
		mux := http.NewServeMux()
		metrics := observability.NewRegistry()
		mux.Handle("/metrics", metrics.Handler())
		health.NewHandler(2*time.Second, map[health.Dependency]health.Checker{
			health.MongoDB: health.Pending(),
			health.NATS:    health.Pending(),
			health.Dex:     health.Pending(),
		}).Routes(mux)
		limiter := observability.NewRateLimiter(120, time.Minute)
		handler := observability.HTTPMiddleware(metrics, observability.RateLimitMiddleware(limiter, mux))
		services = append(services, httpapi.New(cfg.HTTPAddress, handler, cfg.ShutdownTimeout))
	}
	if cfg.Mode == config.ModeWorker || cfg.Mode == config.ModeAll {
		services = append(services, idleService("worker"))
	}
	return newWithServices(logger, cfg.ShutdownTimeout, services...)
}

func newWithServices(logger *slog.Logger, timeout time.Duration, services ...Service) *App {
	return &App{logger: logger, shutdownTimeout: timeout, services: services}
}

// Run blocks until cancellation or a service failure, then gives every
// service a bounded interval to exit.
func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, len(a.services))
	for _, service := range a.services {
		service := service
		a.logger.Info("service starting", "service", service.Name())
		go func() {
			err := service.Run(runCtx)
			if err != nil && !errors.Is(err, context.Canceled) {
				results <- fmt.Errorf("service %s: %w", service.Name(), err)
				return
			}
			results <- nil
		}()
	}

	var runErr error
	remaining := len(a.services)
	select {
	case <-ctx.Done():
		a.logger.Info("shutdown requested")
	case runErr = <-results:
		remaining--
		if runErr == nil && ctx.Err() == nil {
			runErr = errors.New("service stopped unexpectedly")
		}
	}
	cancel()

	timer := time.NewTimer(a.shutdownTimeout)
	defer timer.Stop()
	for ; remaining > 0; remaining-- {
		select {
		case <-results:
		case <-timer.C:
			return errors.Join(runErr, errors.New("graceful shutdown timed out"))
		}
	}

	a.logger.Info("shutdown complete")
	return runErr
}

type idleService string

func (s idleService) Name() string { return string(s) }

func (s idleService) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
