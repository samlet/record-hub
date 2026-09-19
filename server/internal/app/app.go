// Package app coordinates process responsibilities and graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/config"
	"github.com/samlet/record-hub/server/internal/health"
	"github.com/samlet/record-hub/server/internal/httpapi"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/observability"
	"github.com/samlet/record-hub/server/internal/web"
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

// New assembles the responsibilities selected by mode. When runtime
// dependency URLs are configured it wires the Mongo repositories, NATS
// durable consumers, and resource handlers; without them the dependency-free
// health/auth boundary remains available for contract smoke tests.
func New(cfg config.Config, logger *slog.Logger) *App {
	services := make([]Service, 0, 2)
	if cfg.Mode == config.ModeAPI || cfg.Mode == config.ModeAll {
		mux := http.NewServeMux()
		metrics := observability.NewRegistry()
		mux.Handle("/metrics", metrics.Handler())
		runtime, runtimeErr := newRuntime(cfg, metrics, logger)
		if runtimeErr != nil {
			services = append(services, failedService{err: runtimeErr})
			runtime = &runtimeDependencies{checks: map[health.Dependency]health.Checker{health.MongoDB: health.Pending(), health.NATS: health.Pending(), health.Dex: health.Pending()}}
		}
		health.NewHandler(2*time.Second, runtime.checks).Routes(mux)
		var webMiddleware func(http.Handler) http.Handler
		if cfg.Web.Enabled {
			sessions, err := web.NewSessionManager([]byte(cfg.Web.SessionSecret), cfg.Web.SessionTTL, cfg.Web.SecureCookies)
			if err != nil {
				services = append(services, failedService{err: fmt.Errorf("configure web session: %w", err)})
			} else {
				verifier := web.NewLazyNonceVerifier(identity.OIDCVerifierConfig{
					Issuer:              cfg.Web.Issuer,
					Audience:            cfg.Web.Audience,
					PrincipalKind:       identity.PrincipalUser,
					AllowInsecureIssuer: cfg.Web.AllowInsecureEndpoints,
				})
				exchanger := web.HTTPCodeExchanger{
					TokenEndpoint:         cfg.Web.TokenEndpoint,
					ClientID:              cfg.Web.ClientID,
					ClientSecret:          cfg.Web.ClientSecret,
					RedirectURL:           cfg.Web.RedirectURL,
					AllowInsecureEndpoint: cfg.Web.AllowInsecureEndpoints,
					Verifier:              verifier,
				}
				auth, err := web.NewHandler(web.Config{
					AuthorizationEndpoint:  cfg.Web.AuthorizationEndpoint,
					ClientID:               cfg.Web.ClientID,
					RedirectURL:            cfg.Web.RedirectURL,
					SuccessRedirectURL:     cfg.Web.SuccessRedirectURL,
					PostLogoutRedirectURL:  cfg.Web.PostLogoutRedirectURL,
					Scope:                  cfg.Web.Scope,
					AllowInsecureEndpoints: cfg.Web.AllowInsecureEndpoints,
					SecureCookies:          cfg.Web.SecureCookies,
					SessionTTL:             cfg.Web.SessionTTL,
				}, sessions, exchanger.Exchange)
				if err != nil {
					services = append(services, failedService{err: fmt.Errorf("configure web auth: %w", err)})
				} else {
					auth.Routes(mux)
					webMiddleware = auth.Middleware
				}
			}
		}
		rateLimitPerMinute := 120
		if raw := strings.TrimSpace(os.Getenv("RECORD_HUB_HTTP_RATE_LIMIT_PER_MINUTE")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100_000 {
				rateLimitPerMinute = parsed
			}
		}
		limiter := observability.NewRateLimiter(rateLimitPerMinute, time.Minute)
		resourceHandler := web.NewConsoleRouter(runtime.records, runtime.schema, runtime.operations, runtime.catalog, runtime.rebuild, runtime.feed)
		if runtime.binding != nil {
			mux.Handle("/api/v1/bindings/", runtime.binding)
		}
		if runtime.commands != nil {
			mux.Handle("/api/v1/commands", runtime.commands)
			mux.Handle("/api/v1/commands/", runtime.commands)
		}
		if runtime.commandOps != nil {
			mux.Handle("/api/v1/operations/commands", runtime.commandOps)
		}
		if runtime.associations != nil {
			mux.Handle("/api/v1/associations/", runtime.associations)
		}
		mux.Handle("/", resourceHandler)
		handler := observability.HTTPMiddleware(metrics, observability.RateLimitMiddleware(limiter, mux))
		if webMiddleware != nil {
			handler = webMiddleware(handler)
		}
		if runtime.webVerifier != nil {
			handler = identity.BearerMiddleware(runtime.webVerifier, handler)
		}
		services = append(services, httpapi.New(cfg.HTTPAddress, handler, cfg.ShutdownTimeout))
		if runtime.closer != nil {
			services = append(services, runtime.closer)
		}
		if cfg.Mode == config.ModeAll {
			services = append(services, runtime.workers...)
		}
	}
	if cfg.Mode == config.ModeWorker {
		metrics := observability.NewRegistry()
		runtime, runtimeErr := newRuntime(cfg, metrics, logger)
		if runtimeErr != nil {
			services = append(services, failedService{err: runtimeErr})
		} else {
			if len(runtime.workers) == 0 {
				services = append(services, idleService("worker"))
			} else {
				services = append(services, runtime.workers...)
			}
			if runtime.closer != nil {
				services = append(services, runtime.closer)
			}
		}
	}
	return newWithServices(logger, cfg.ShutdownTimeout, services...)
}

// failedService turns an invalid optional subsystem configuration into a
// visible startup failure while retaining App's historical constructor API.
type failedService struct{ err error }

func (s failedService) Name() string              { return "configuration" }
func (s failedService) Run(context.Context) error { return s.err }

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
