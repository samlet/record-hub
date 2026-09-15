package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRunStopsServicesOnCancellation(t *testing.T) {
	started := make(chan struct{})
	service := stubService{name: "test", run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	application := newWithServices(discardLogger(), time.Second, service)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	<-started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunPropagatesServiceFailure(t *testing.T) {
	want := errors.New("boom")
	service := stubService{name: "test", run: func(context.Context) error { return want }}
	application := newWithServices(discardLogger(), time.Second, service)

	err := application.Run(context.Background())
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "service test") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunBoundsShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	service := stubService{name: "stuck", run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}
	application := newWithServices(discardLogger(), 10*time.Millisecond, service)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	<-started
	cancel()

	err := <-done
	close(release)
	if err == nil || !strings.Contains(err.Error(), "graceful shutdown timed out") {
		t.Fatalf("Run() error = %v", err)
	}
}

type stubService struct {
	name string
	run  func(context.Context) error
}

func (s stubService) Name() string                  { return s.name }
func (s stubService) Run(ctx context.Context) error { return s.run(ctx) }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
