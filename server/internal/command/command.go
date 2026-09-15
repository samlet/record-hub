// Package command implements the record-hub command-line entry point.
package command

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const usage = `Record Hub workflow data middleware

Usage:
  record-hub help
  record-hub version
`

// Version is replaced at build time for release artifacts.
var Version = "dev"

// Run dispatches a record-hub command. Runtime commands are added as their
// configuration and lifecycle contracts are implemented.
func Run(ctx context.Context, args []string, stdout, _ io.Writer) error {
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
	default:
		return fmt.Errorf("unknown command %q; run record-hub help: %w", args[0], ErrUsage)
	}
}

// ErrUsage identifies invalid command-line input.
var ErrUsage = errors.New("invalid command usage")
