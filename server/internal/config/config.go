// Package config loads and validates Record Hub runtime configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
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
const defaultWebSessionTTL = 8 * time.Hour

// WebAuthConfig contains the optional browser/BFF OIDC boundary. It is kept
// separate from API bearer-token configuration so a Web client secret can
// never be accidentally reused as a service credential.
type WebAuthConfig struct {
	Enabled                bool
	Issuer                 string
	Audience               string
	AuthorizationEndpoint  string
	TokenEndpoint          string
	ClientID               string
	ClientSecret           string
	RedirectURL            string
	SuccessRedirectURL     string
	PostLogoutRedirectURL  string
	Scope                  string
	SessionSecret          string
	SessionTTL             time.Duration
	SecureCookies          bool
	AllowInsecureEndpoints bool
}

// Config contains process-level runtime configuration.
type Config struct {
	Mode            Mode
	HTTPAddress     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	Web             WebAuthConfig
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

	if enabled, present, err := optionalBool(lookup, "RECORD_HUB_WEB_ENABLED"); err != nil {
		errs = append(errs, err)
	} else if present {
		cfg.Web.Enabled = enabled
	}
	if cfg.Web.Enabled {
		if cfg.Mode != ModeAPI && cfg.Mode != ModeAll {
			errs = append(errs, errors.New("RECORD_HUB_WEB_ENABLED requires RECORD_HUB_MODE=api or all"))
		}
		loadWebConfig(lookup, &cfg.Web, &errs)
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func loadWebConfig(lookup lookupEnv, cfg *WebAuthConfig, errs *[]error) {
	value := func(key string) string {
		v, ok := required(lookup, key)
		if !ok {
			*errs = append(*errs, fmt.Errorf("%s is required when RECORD_HUB_WEB_ENABLED=true", key))
		}
		return v
	}
	cfg.Issuer = value("RECORD_HUB_WEB_ISSUER")
	cfg.Audience = value("RECORD_HUB_WEB_AUDIENCE")
	cfg.AuthorizationEndpoint = value("RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT")
	cfg.TokenEndpoint = value("RECORD_HUB_WEB_TOKEN_ENDPOINT")
	cfg.ClientID = value("RECORD_HUB_WEB_CLIENT_ID")
	cfg.ClientSecret = value("RECORD_HUB_WEB_CLIENT_SECRET")
	cfg.RedirectURL = value("RECORD_HUB_WEB_REDIRECT_URL")
	cfg.SessionSecret = value("RECORD_HUB_WEB_SESSION_SECRET")
	if len(cfg.SessionSecret) < 32 {
		*errs = append(*errs, errors.New("RECORD_HUB_WEB_SESSION_SECRET must contain at least 32 bytes"))
	}
	cfg.SuccessRedirectURL, _ = optional(lookup, "RECORD_HUB_WEB_SUCCESS_REDIRECT_URL")
	cfg.PostLogoutRedirectURL, _ = optional(lookup, "RECORD_HUB_WEB_POST_LOGOUT_REDIRECT_URL")
	cfg.Scope, _ = optional(lookup, "RECORD_HUB_WEB_SCOPE")
	cfg.SessionTTL = defaultWebSessionTTL
	if raw, present := optional(lookup, "RECORD_HUB_WEB_SESSION_TTL"); present {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 || duration > 24*time.Hour {
			*errs = append(*errs, fmt.Errorf("RECORD_HUB_WEB_SESSION_TTL must be between 1 second and 24 hours; got %q", raw))
		} else {
			cfg.SessionTTL = duration
		}
	}
	if value, present, err := optionalBool(lookup, "RECORD_HUB_WEB_SECURE_COOKIES"); err != nil {
		*errs = append(*errs, err)
	} else if present {
		cfg.SecureCookies = value
	} else {
		*errs = append(*errs, errors.New("RECORD_HUB_WEB_SECURE_COOKIES is required when RECORD_HUB_WEB_ENABLED=true"))
	}
	if value, present, err := optionalBool(lookup, "RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS"); err != nil {
		*errs = append(*errs, err)
	} else if present {
		cfg.AllowInsecureEndpoints = value
	}
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

func optionalBool(lookup lookupEnv, key string) (bool, bool, error) {
	value, present := optional(lookup, key)
	if !present {
		return false, false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, true, fmt.Errorf("%s must be true or false; got %q", key, value)
	}
	return parsed, true, nil
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
