package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := Run(context.Background(), nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "record-hub version") {
		t.Fatalf("Run() output = %q, want usage", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	previous := Version
	Version = "test-version"
	t.Cleanup(func() { Version = previous })

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"version"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "test-version\n"; got != want {
		t.Fatalf("Run() output = %q, want %q", got, want)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), []string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("Run() error = %v, want ErrUsage", err)
	}
}

func TestRunCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
