package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadAllMode(t *testing.T) {
	env := map[string]string{
		"RECORD_HUB_MODE":             "all",
		"RECORD_HUB_HTTP_ADDRESS":     "127.0.0.1:8080",
		"RECORD_HUB_LOG_LEVEL":        "debug",
		"RECORD_HUB_SHUTDOWN_TIMEOUT": "3s",
	}

	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Mode != ModeAll || cfg.HTTPAddress != "127.0.0.1:8080" {
		t.Fatalf("load() config = %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("load() controls = %+v", cfg)
	}
}

func TestLoadWorkerDefaults(t *testing.T) {
	cfg, err := load(mapLookup(map[string]string{"RECORD_HUB_MODE": "worker"}))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.HTTPAddress != "" || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("load() config = %+v", cfg)
	}
}

func TestLoadWebAuthConfig(t *testing.T) {
	env := map[string]string{
		"RECORD_HUB_MODE":                         "api",
		"RECORD_HUB_HTTP_ADDRESS":                 "127.0.0.1:8080",
		"RECORD_HUB_WEB_ENABLED":                  "true",
		"RECORD_HUB_WEB_ISSUER":                   "http://127.0.0.1:5556/dex",
		"RECORD_HUB_WEB_AUDIENCE":                 "record-hub-web-local",
		"RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT":   "http://127.0.0.1:5556/dex/auth",
		"RECORD_HUB_WEB_TOKEN_ENDPOINT":           "http://127.0.0.1:5556/dex/token",
		"RECORD_HUB_WEB_CLIENT_ID":                "record-hub-web-local",
		"RECORD_HUB_WEB_CLIENT_SECRET":            "secret",
		"RECORD_HUB_WEB_REDIRECT_URL":             "http://127.0.0.1:3004/auth/callback",
		"RECORD_HUB_WEB_SESSION_SECRET":           "01234567890123456789012345678901",
		"RECORD_HUB_WEB_SESSION_TTL":              "2h",
		"RECORD_HUB_WEB_SECURE_COOKIES":           "false",
		"RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS": "true",
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if !cfg.Web.Enabled || cfg.Web.Issuer == "" || cfg.Web.SessionTTL != 2*time.Hour || !cfg.Web.AllowInsecureEndpoints {
		t.Fatalf("web config = %+v", cfg.Web)
	}
}

func TestLoadWebAuthRequiresSecretsAndEndpoints(t *testing.T) {
	_, err := load(mapLookup(map[string]string{
		"RECORD_HUB_MODE":         "api",
		"RECORD_HUB_HTTP_ADDRESS": "127.0.0.1:8080",
		"RECORD_HUB_WEB_ENABLED":  "true",
	}))
	if err == nil || !strings.Contains(err.Error(), "RECORD_HUB_WEB_ISSUER is required") || !strings.Contains(err.Error(), "RECORD_HUB_WEB_SESSION_SECRET is required") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadRejectsMissingAndInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "missing mode", env: nil, want: "RECORD_HUB_MODE is required"},
		{name: "invalid mode", env: map[string]string{"RECORD_HUB_MODE": "both"}, want: "must be api, worker, or all"},
		{name: "missing address", env: map[string]string{"RECORD_HUB_MODE": "api"}, want: "RECORD_HUB_HTTP_ADDRESS is required"},
		{name: "invalid address", env: map[string]string{"RECORD_HUB_MODE": "api", "RECORD_HUB_HTTP_ADDRESS": "8080"}, want: "host:port"},
		{name: "invalid level", env: map[string]string{"RECORD_HUB_MODE": "worker", "RECORD_HUB_LOG_LEVEL": "verbose"}, want: "RECORD_HUB_LOG_LEVEL"},
		{name: "invalid timeout", env: map[string]string{"RECORD_HUB_MODE": "worker", "RECORD_HUB_SHUTDOWN_TIMEOUT": "0s"}, want: "positive duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(mapLookup(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("load() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func mapLookup(values map[string]string) lookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
