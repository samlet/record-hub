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

func TestLoadQueryAndProjectionSLOBudgets(t *testing.T) {
	cfg, err := load(mapLookup(map[string]string{
		"RECORD_HUB_MODE":                       "worker",
		"RECORD_HUB_QUERY_MAX_PAGE_ROWS":        "80",
		"RECORD_HUB_QUERY_MAX_RESPONSE_BYTES":   "2097152",
		"RECORD_HUB_QUERY_MAX_DURATION":         "1500ms",
		"RECORD_HUB_PROJECTION_BACKLOG_WARNING": "25",
		"RECORD_HUB_PROJECTION_LAG_WARNING":     "45s",
		"RECORD_HUB_PROJECTION_FAILURE_BUDGET":  "7",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QueryBudget.MaxPageRows != 80 || cfg.QueryBudget.MaxResponseBytes != 2097152 || cfg.QueryBudget.MaxDuration != 1500*time.Millisecond {
		t.Fatalf("query budget = %+v", cfg.QueryBudget)
	}
	if cfg.ProjectionSLO.BacklogWarning != 25 || cfg.ProjectionSLO.LagWarning != 45*time.Second || cfg.ProjectionSLO.FailureBudget != 7 {
		t.Fatalf("projection SLO = %+v", cfg.ProjectionSLO)
	}
}

func TestLoadRejectsUnboundedQueryBudget(t *testing.T) {
	_, err := load(mapLookup(map[string]string{"RECORD_HUB_MODE": "worker", "RECORD_HUB_QUERY_MAX_RESPONSE_BYTES": "1"}))
	if err == nil || !strings.Contains(err.Error(), "RECORD_HUB_QUERY_MAX_RESPONSE_BYTES") {
		t.Fatalf("query budget error = %v", err)
	}
}

func TestLoadRuntimeDependencyConfig(t *testing.T) {
	env := map[string]string{
		"RECORD_HUB_MODE":                       "all",
		"RECORD_HUB_HTTP_ADDRESS":               "127.0.0.1:8080",
		"RECORD_HUB_MONGODB_URI":                "mongodb://127.0.0.1:27017/record_hub",
		"RECORD_HUB_MONGODB_DATABASE":           "record_hub_local",
		"RECORD_HUB_NATS_URL":                   "nats://127.0.0.1:4222",
		"RECORD_HUB_PROJECTION_WORKSPACE_ID":    "workspace-local",
		"RECORD_HUB_PROJECTION_WORKSPACE_MAP":   `{"tenant-a":"workspace-a","tenant-b":"workspace-b"}`,
		"RECORD_HUB_OIDC_ISSUER":                "http://127.0.0.1:5556",
		"RECORD_HUB_OIDC_AUDIENCE":              "record-hub-api-local",
		"RECORD_HUB_OIDC_PRINCIPAL_KIND":        "user",
		"RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER": "true",
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.MongoURI == "" || cfg.MongoDatabase != "record_hub_local" || cfg.NATSURL == "" || cfg.ProjectionWorkspaceID != "workspace-local" {
		t.Fatalf("runtime config = %+v", cfg)
	}
	if len(cfg.ProjectionWorkspaceMappings) != 2 || cfg.ProjectionWorkspaceMappings["tenant-a"] != "workspace-a" {
		t.Fatalf("workspace mappings = %+v", cfg.ProjectionWorkspaceMappings)
	}
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCPrincipalKind != "user" || !cfg.OIDCAllowInsecureIssuer {
		t.Fatalf("OIDC config = %+v", cfg)
	}
}

func TestLoadBindingMachinePolicies(t *testing.T) {
	const issuer = "http://127.0.0.1:15557/workload"
	env := map[string]string{
		"RECORD_HUB_MODE":                "api",
		"RECORD_HUB_HTTP_ADDRESS":        "127.0.0.1:18080",
		"RECORD_HUB_OIDC_ISSUER":         issuer,
		"RECORD_HUB_OIDC_AUDIENCE":       "record-hub-api",
		"RECORD_HUB_OIDC_PRINCIPAL_KIND": "service",
		"RECORD_HUB_BINDING_MACHINE_POLICIES": `[
			{"issuer":"http://127.0.0.1:15557/workload","subject":"fluxion-to-record-hub","audience":"record-hub-api","scope":"recordhub.binding.snapshot","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT"}
		]`,
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if len(cfg.BindingMachinePolicies) != 1 || cfg.BindingMachinePolicies[0].Subject != "fluxion-to-record-hub" {
		t.Fatalf("machine policies = %+v", cfg.BindingMachinePolicies)
	}
}

func TestLoadCommandPolicies(t *testing.T) {
	env := map[string]string{
		"RECORD_HUB_MODE":                "api",
		"RECORD_HUB_HTTP_ADDRESS":        "127.0.0.1:18080",
		"RECORD_HUB_OIDC_ISSUER":         "http://127.0.0.1:15557/workload",
		"RECORD_HUB_OIDC_AUDIENCE":       "record-hub-api",
		"RECORD_HUB_OIDC_PRINCIPAL_KIND": "service",
		"RECORD_HUB_COMMAND_POLICIES": `[
			{"policyId":"project.annotate","issuer":"http://127.0.0.1:15557/workload","subject":"fluxion","audience":"record-hub-api","scope":"recordhub.command.submit","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"project-annotation","ownerSystem":"fluxion","resourceType":"PROJECT","action":"project.annotate","expectedVersionRequired":true}
		]`,
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CommandPolicies) != 1 || cfg.CommandPolicies[0].PolicyID != "project.annotate" || cfg.CommandPolicies[0].MaxPayloadBytes != 256<<10 {
		t.Fatalf("command policies = %+v", cfg.CommandPolicies)
	}
}

func TestLoadRejectsUnsafeBindingMachinePolicies(t *testing.T) {
	base := map[string]string{
		"RECORD_HUB_MODE":                "api",
		"RECORD_HUB_HTTP_ADDRESS":        "127.0.0.1:18080",
		"RECORD_HUB_OIDC_ISSUER":         "https://workload.example.test",
		"RECORD_HUB_OIDC_AUDIENCE":       "record-hub-api",
		"RECORD_HUB_OIDC_PRINCIPAL_KIND": "service",
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: `[]`, want: "at least one"},
		{name: "wildcard", raw: `[{"issuer":"https://workload.example.test","subject":"*","audience":"record-hub-api","scope":"recordhub.binding.snapshot","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT"}]`, want: "without wildcards"},
		{name: "wrong issuer", raw: `[{"issuer":"https://other.example.test","subject":"fluxion","audience":"record-hub-api","scope":"recordhub.binding.snapshot","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT"}]`, want: "issuer must match"},
		{name: "wrong audience", raw: `[{"issuer":"https://workload.example.test","subject":"fluxion","audience":"other-api","scope":"recordhub.binding.snapshot","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT"}]`, want: "audience must match"},
		{name: "unknown field", raw: `[{"issuer":"https://workload.example.test","subject":"fluxion","audience":"record-hub-api","scope":"recordhub.binding.snapshot","tenantId":"tenant-1","workspaceId":"workspace-1","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT","allowAll":true}]`, want: "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+1)
			for key, value := range base {
				env[key] = value
			}
			env["RECORD_HUB_BINDING_MACHINE_POLICIES"] = tt.raw
			_, err := load(mapLookup(env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("load() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsMachinePoliciesWithoutServiceOIDC(t *testing.T) {
	_, err := load(mapLookup(map[string]string{
		"RECORD_HUB_MODE":                     "api",
		"RECORD_HUB_HTTP_ADDRESS":             "127.0.0.1:18080",
		"RECORD_HUB_BINDING_MACHINE_POLICIES": `[{"issuer":"https://issuer","subject":"service","audience":"api","scope":"recordhub.binding.snapshot","tenantId":"tenant","workspaceId":"workspace","purpose":"diagnostic","resourceSystem":"fluxion","resourceType":"PROJECT"}]`,
	}))
	if err == nil || !strings.Contains(err.Error(), "requires bearer OIDC") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadRejectsInvalidProjectionWorkspaceMap(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "not json", raw: "tenant-a=workspace-a", want: "must be a JSON object"},
		{name: "null", raw: "null", want: "must be a JSON object"},
		{name: "empty workspace", raw: `{"tenant-a":""}`, want: "workspace IDs must be non-empty"},
		{name: "whitespace tenant", raw: `{"tenant a":"workspace-a"}`, want: "tenant IDs must be non-empty"},
		{name: "normalized duplicate", raw: `{"tenant-a":"workspace-a"," tenant-a ":"workspace-b"}`, want: "duplicate tenant ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(mapLookup(map[string]string{
				"RECORD_HUB_MODE":                     "worker",
				"RECORD_HUB_PROJECTION_WORKSPACE_MAP": tt.raw,
			}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("load() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsPartialBearerOIDCConfig(t *testing.T) {
	_, err := load(mapLookup(map[string]string{
		"RECORD_HUB_MODE":        "worker",
		"RECORD_HUB_OIDC_ISSUER": "http://127.0.0.1:5556",
	}))
	if err == nil || !strings.Contains(err.Error(), "required together") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadWorkerDefaults(t *testing.T) {
	cfg, err := load(mapLookup(map[string]string{"RECORD_HUB_MODE": "worker"}))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Environment != "local" || cfg.HTTPAddress != "" || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("load() config = %+v", cfg)
	}
}

func TestLoadProductionRejectsInsecureIdentitySettings(t *testing.T) {
	base := map[string]string{
		"RECORD_HUB_MODE":                         "api",
		"RECORD_HUB_HTTP_ADDRESS":                 "127.0.0.1:8080",
		"RECORD_HUB_ENVIRONMENT":                  "production",
		"RECORD_HUB_OIDC_ISSUER":                  "http://dex.internal",
		"RECORD_HUB_OIDC_AUDIENCE":                "record-hub-api",
		"RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER":   "true",
		"RECORD_HUB_WEB_ENABLED":                  "true",
		"RECORD_HUB_WEB_ISSUER":                   "http://dex.internal",
		"RECORD_HUB_WEB_AUDIENCE":                 "record-hub-web",
		"RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT":   "http://dex.internal/auth",
		"RECORD_HUB_WEB_TOKEN_ENDPOINT":           "http://dex.internal/token",
		"RECORD_HUB_WEB_CLIENT_ID":                "record-hub-web",
		"RECORD_HUB_WEB_CLIENT_SECRET":            "secret",
		"RECORD_HUB_WEB_REDIRECT_URL":             "https://record-hub.example/callback",
		"RECORD_HUB_WEB_SESSION_SECRET":           "01234567890123456789012345678901",
		"RECORD_HUB_WEB_SECURE_COOKIES":           "false",
		"RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS": "true",
	}
	_, err := load(mapLookup(base))
	if err == nil || !strings.Contains(err.Error(), "must be false in production") || !strings.Contains(err.Error(), "must be true in production") {
		t.Fatalf("production identity error = %v", err)
	}
}

func TestLoadProductionAcceptsSecureIdentitySettings(t *testing.T) {
	env := map[string]string{
		"RECORD_HUB_MODE":                         "api",
		"RECORD_HUB_HTTP_ADDRESS":                 "127.0.0.1:8080",
		"RECORD_HUB_ENVIRONMENT":                  "production",
		"RECORD_HUB_OIDC_ISSUER":                  "https://dex.internal",
		"RECORD_HUB_OIDC_AUDIENCE":                "record-hub-api",
		"RECORD_HUB_WEB_ENABLED":                  "true",
		"RECORD_HUB_WEB_ISSUER":                   "https://dex.internal",
		"RECORD_HUB_WEB_AUDIENCE":                 "record-hub-web",
		"RECORD_HUB_WEB_AUTHORIZATION_ENDPOINT":   "https://dex.internal/auth",
		"RECORD_HUB_WEB_TOKEN_ENDPOINT":           "https://dex.internal/token",
		"RECORD_HUB_WEB_CLIENT_ID":                "record-hub-web",
		"RECORD_HUB_WEB_CLIENT_SECRET":            "secret",
		"RECORD_HUB_WEB_REDIRECT_URL":             "https://record-hub.example/callback",
		"RECORD_HUB_WEB_SESSION_SECRET":           "01234567890123456789012345678901",
		"RECORD_HUB_WEB_SECURE_COOKIES":           "true",
		"RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS": "false",
	}
	cfg, err := load(mapLookup(env))
	if err != nil {
		t.Fatalf("secure production config error = %v", err)
	}
	if cfg.Environment != "production" || !cfg.Web.SecureCookies || cfg.Web.AllowInsecureEndpoints {
		t.Fatalf("production identity config = %+v", cfg)
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
