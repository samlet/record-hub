// Package command implements the record-hub command-line entry point.
package command

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/samlet/record-hub/server/internal/app"
	"github.com/samlet/record-hub/server/internal/config"
	"github.com/samlet/record-hub/server/internal/logging"
)

const usage = `Record Hub workflow data middleware

Usage:
  record-hub help
  record-hub version
  record-hub serve
`

// Version is replaced at build time for release artifacts.
var Version = "dev"

// Run dispatches a record-hub command. Runtime commands are added as their
// configuration and lifecycle contracts are implemented.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(args) == 0 {
		_, err := io.WriteString(stdout, usage)
		return err
	}

	switch args[0] {
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, usage)
		return err
	case "version", "-v", "--version":
		_, err := fmt.Fprintln(stdout, Version)
		return err
	case "serve":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		logger := logging.New(stderr, cfg.LogLevel)
		logger.Info("record hub starting", "mode", cfg.Mode, "version", Version)
		return app.New(cfg, logger).Run(ctx)
	default:
		return fmt.Errorf("unknown command %q; run record-hub help: %w", args[0], ErrUsage)
	}
}

// ErrUsage identifies invalid command-line input.
var ErrUsage = errors.New("invalid command usage")
