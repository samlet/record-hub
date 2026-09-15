// Package config loads and validates Record Hub runtime configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

// Mode selects which process responsibilities are enabled.
type Mode string

const (
	ModeAPI    Mode = "api"
	ModeWorker Mode = "worker"
	ModeAll    Mode = "all"
)

const defaultShutdownTimeout = 10 * time.Second

// Config contains process-level runtime configuration.
type Config struct {
	Mode            Mode
	HTTPAddress     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// Load reads configuration from the process environment and fails closed when
// a required or security-relevant value is absent or invalid.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv func(string) (string, bool)

func load(lookup lookupEnv) (Config, error) {
	var cfg Config
	var errs []error

	mode, ok := required(lookup, "RECORD_HUB_MODE")
	if !ok {
		errs = append(errs, errors.New("RECORD_HUB_MODE is required"))
	} else {
		cfg.Mode = Mode(strings.ToLower(mode))
		if cfg.Mode != ModeAPI && cfg.Mode != ModeWorker && cfg.Mode != ModeAll {
			errs = append(errs, fmt.Errorf("RECORD_HUB_MODE must be api, worker, or all; got %q", mode))
		}
	}

	if cfg.Mode == ModeAPI || cfg.Mode == ModeAll {
		address, present := required(lookup, "RECORD_HUB_HTTP_ADDRESS")
		if !present {
			errs = append(errs, errors.New("RECORD_HUB_HTTP_ADDRESS is required in api and all modes"))
		} else if err := validateAddress(address); err != nil {
			errs = append(errs, fmt.Errorf("RECORD_HUB_HTTP_ADDRESS: %w", err))
		} else {
			cfg.HTTPAddress = address
		}
	}

	cfg.LogLevel = slog.LevelInfo
	if value, present := optional(lookup, "RECORD_HUB_LOG_LEVEL"); present {
		if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(value))); err != nil {
			errs = append(errs, fmt.Errorf("RECORD_HUB_LOG_LEVEL: %w", err))
		}
	}

	cfg.ShutdownTimeout = defaultShutdownTimeout
	if value, present := optional(lookup, "RECORD_HUB_SHUTDOWN_TIMEOUT"); present {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			errs = append(errs, fmt.Errorf("RECORD_HUB_SHUTDOWN_TIMEOUT must be a positive duration; got %q", value))
		} else {
			cfg.ShutdownTimeout = duration
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func required(lookup lookupEnv, key string) (string, bool) {
	value, ok := lookup(key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func optional(lookup lookupEnv, key string) (string, bool) {
	value, ok := lookup(key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("must use host:port form")
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return errors.New("host and port must both be set")
	}
	return nil
}
