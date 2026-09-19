// Package config loads and validates Record Hub runtime configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const (
	defaultQueryMaxPageRows         = 100
	defaultQueryMaxResponseBytes    = 1 << 20
	defaultQueryMaxDuration         = 2 * time.Second
	defaultProjectionBacklogWarning = 100
	defaultProjectionLagWarning     = 30 * time.Second
	defaultProjectionFailureBudget  = 10
)

type QueryBudgetConfig struct {
	MaxPageRows      int
	MaxResponseBytes int64
	MaxDuration      time.Duration
}

type ProjectionSLOConfig struct {
	BacklogWarning int64
	LagWarning     time.Duration
	FailureBudget  int64
}

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

// BindingMachinePolicyConfig is one exact service-to-binding authorization.
// Wildcards are intentionally unsupported: every workload, scope, purpose,
// and resource tuple must be registered explicitly.
type BindingMachinePolicyConfig struct {
	Issuer         string `json:"issuer"`
	Subject        string `json:"subject"`
	Audience       string `json:"audience"`
	Scope          string `json:"scope"`
	TenantID       string `json:"tenantId"`
	WorkspaceID    string `json:"workspaceId"`
	Purpose        string `json:"purpose"`
	ResourceSystem string `json:"resourceSystem"`
	ResourceType   string `json:"resourceType"`
}

// CommandPolicyConfig is an exact workload-to-owner command allowlist entry.
// It is kept separate from binding policies so a snapshot permission cannot
// silently become a write permission.
type CommandPolicyConfig struct {
	PolicyID                string `json:"policyId"`
	Issuer                  string `json:"issuer"`
	Subject                 string `json:"subject"`
	Audience                string `json:"audience"`
	Scope                   string `json:"scope"`
	TenantID                string `json:"tenantId"`
	WorkspaceID             string `json:"workspaceId"`
	Purpose                 string `json:"purpose"`
	OwnerSystem             string `json:"ownerSystem"`
	ResourceType            string `json:"resourceType"`
	Action                  string `json:"action"`
	ExpectedVersionRequired bool   `json:"expectedVersionRequired"`
	MaxPayloadBytes         int    `json:"maxPayloadBytes"`
}

// Config contains process-level runtime configuration.
type Config struct {
	// Environment controls production-only safety checks. Local remains the
	// default so contract smoke tests can use a local Dex endpoint; production
	// rejects insecure OIDC endpoints and non-secure browser cookies.
	Environment     string
	Mode            Mode
	HTTPAddress     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	// MongoURI and MongoDatabase enable the real repository data plane. They
	// remain optional so the dependency-free API boundary can still be used
	// for contract and health checks.
	MongoURI      string
	MongoDatabase string
	// NATSURL enables the durable projection worker. The worker only connects
	// when this value is present; it never silently falls back to an embedded
	// broker.
	NATSURL string
	// ProjectionWorkspaceID is the local fallback for events whose envelope
	// predates an explicit workspace metadata field. Production deployments
	// should prefer an allowlisted per-tenant mapping or include workspaceId in
	// event metadata.
	ProjectionWorkspaceID string
	// ProjectionWorkspaceMappings is an explicit tenant-to-workspace allowlist
	// for legacy events that do not yet carry metadata.workspaceId.
	ProjectionWorkspaceMappings map[string]string
	// Optional bearer OIDC settings. Browser/BFF settings remain in Web.
	OIDCIssuer              string
	OIDCAudience            string
	OIDCPrincipalKind       string
	OIDCAllowInsecureIssuer bool
	BindingMachinePolicies  []BindingMachinePolicyConfig
	CommandPolicies         []CommandPolicyConfig
	QueryBudget             QueryBudgetConfig
	ProjectionSLO           ProjectionSLOConfig
	Web                     WebAuthConfig
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
	cfg.Environment = "local"
	if value, present := optional(lookup, "RECORD_HUB_ENVIRONMENT"); present {
		cfg.Environment = strings.ToLower(strings.TrimSpace(value))
		if cfg.Environment != "local" && cfg.Environment != "staging" && cfg.Environment != "production" {
			errs = append(errs, fmt.Errorf("RECORD_HUB_ENVIRONMENT must be local, staging, or production; got %q", value))
		}
	}

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

	if value, present := optional(lookup, "RECORD_HUB_MONGODB_URI"); present {
		cfg.MongoURI = value
	}
	cfg.MongoDatabase = "record_hub"
	cfg.QueryBudget = QueryBudgetConfig{MaxPageRows: defaultQueryMaxPageRows, MaxResponseBytes: defaultQueryMaxResponseBytes, MaxDuration: defaultQueryMaxDuration}
	cfg.ProjectionSLO = ProjectionSLOConfig{BacklogWarning: defaultProjectionBacklogWarning, LagWarning: defaultProjectionLagWarning, FailureBudget: defaultProjectionFailureBudget}
	if value, present := optional(lookup, "RECORD_HUB_MONGODB_DATABASE"); present {
		cfg.MongoDatabase = value
	}
	if value, present := optional(lookup, "RECORD_HUB_NATS_URL"); present {
		cfg.NATSURL = value
	}
	if value, present := optional(lookup, "RECORD_HUB_QUERY_MAX_PAGE_ROWS"); present {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			errs = append(errs, fmt.Errorf("RECORD_HUB_QUERY_MAX_PAGE_ROWS must be between 1 and 1000; got %q", value))
		} else {
			cfg.QueryBudget.MaxPageRows = parsed
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_QUERY_MAX_RESPONSE_BYTES"); present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1024 || parsed > 16<<20 {
			errs = append(errs, fmt.Errorf("RECORD_HUB_QUERY_MAX_RESPONSE_BYTES must be between 1024 and 16777216; got %q", value))
		} else {
			cfg.QueryBudget.MaxResponseBytes = parsed
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_QUERY_MAX_DURATION"); present {
		duration, err := time.ParseDuration(value)
		if err != nil || duration < 10*time.Millisecond || duration > 30*time.Second {
			errs = append(errs, fmt.Errorf("RECORD_HUB_QUERY_MAX_DURATION must be between 10ms and 30s; got %q", value))
		} else {
			cfg.QueryBudget.MaxDuration = duration
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_PROJECTION_BACKLOG_WARNING"); present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 || parsed > 1_000_000 {
			errs = append(errs, fmt.Errorf("RECORD_HUB_PROJECTION_BACKLOG_WARNING must be between 1 and 1000000; got %q", value))
		} else {
			cfg.ProjectionSLO.BacklogWarning = parsed
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_PROJECTION_LAG_WARNING"); present {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration > 24*time.Hour {
			errs = append(errs, fmt.Errorf("RECORD_HUB_PROJECTION_LAG_WARNING must be between 1ms and 24h; got %q", value))
		} else {
			cfg.ProjectionSLO.LagWarning = duration
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_PROJECTION_FAILURE_BUDGET"); present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 || parsed > 1_000_000 {
			errs = append(errs, fmt.Errorf("RECORD_HUB_PROJECTION_FAILURE_BUDGET must be between 1 and 1000000; got %q", value))
		} else {
			cfg.ProjectionSLO.FailureBudget = parsed
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_PROJECTION_WORKSPACE_ID"); present {
		cfg.ProjectionWorkspaceID = value
	}
	if value, present := optional(lookup, "RECORD_HUB_PROJECTION_WORKSPACE_MAP"); present {
		mappings, err := parseProjectionWorkspaceMappings(value)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.ProjectionWorkspaceMappings = mappings
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_OIDC_ISSUER"); present {
		cfg.OIDCIssuer = value
	}
	if value, present := optional(lookup, "RECORD_HUB_OIDC_AUDIENCE"); present {
		cfg.OIDCAudience = value
	}
	if value, present := optional(lookup, "RECORD_HUB_OIDC_PRINCIPAL_KIND"); present {
		cfg.OIDCPrincipalKind = strings.ToLower(value)
	}
	if value, present, err := optionalBool(lookup, "RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER"); err != nil {
		errs = append(errs, err)
	} else if present {
		cfg.OIDCAllowInsecureIssuer = value
	}
	if cfg.OIDCIssuer != "" || cfg.OIDCAudience != "" || cfg.OIDCPrincipalKind != "" {
		if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" {
			errs = append(errs, errors.New("RECORD_HUB_OIDC_ISSUER and RECORD_HUB_OIDC_AUDIENCE are required together"))
		}
		if cfg.OIDCPrincipalKind == "" {
			cfg.OIDCPrincipalKind = "user"
		}
		if cfg.OIDCPrincipalKind != "user" && cfg.OIDCPrincipalKind != "service" {
			errs = append(errs, errors.New("RECORD_HUB_OIDC_PRINCIPAL_KIND must be user or service"))
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_BINDING_MACHINE_POLICIES"); present {
		policies, err := parseBindingMachinePolicies(value, cfg)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.BindingMachinePolicies = policies
		}
	}
	if value, present := optional(lookup, "RECORD_HUB_COMMAND_POLICIES"); present {
		policies, err := parseCommandPolicies(value, cfg)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.CommandPolicies = policies
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
	if cfg.Environment == "production" {
		if cfg.OIDCAllowInsecureIssuer {
			errs = append(errs, errors.New("RECORD_HUB_OIDC_ALLOW_INSECURE_ISSUER must be false in production"))
		}
		if cfg.Web.Enabled {
			if cfg.Web.AllowInsecureEndpoints {
				errs = append(errs, errors.New("RECORD_HUB_WEB_ALLOW_INSECURE_ENDPOINTS must be false in production"))
			}
			if !cfg.Web.SecureCookies {
				errs = append(errs, errors.New("RECORD_HUB_WEB_SECURE_COOKIES must be true in production"))
			}
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func parseCommandPolicies(raw string, cfg Config) ([]CommandPolicyConfig, error) {
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCPrincipalKind != "service" {
		return nil, errors.New("RECORD_HUB_COMMAND_POLICIES requires bearer OIDC with principal kind service")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var policies []CommandPolicyConfig
	if err := decoder.Decode(&policies); err != nil {
		return nil, fmt.Errorf("RECORD_HUB_COMMAND_POLICIES must be a JSON array of exact policies: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		return nil, errors.New("RECORD_HUB_COMMAND_POLICIES must contain one JSON array")
	}
	if len(policies) == 0 || len(policies) > 128 {
		return nil, errors.New("RECORD_HUB_COMMAND_POLICIES must contain between 1 and 128 policies")
	}
	seen := make(map[string]struct{}, len(policies))
	for index := range policies {
		policy := &policies[index]
		policy.PolicyID = strings.TrimSpace(policy.PolicyID)
		policy.Issuer = strings.TrimSpace(policy.Issuer)
		policy.Subject = strings.TrimSpace(policy.Subject)
		policy.Audience = strings.TrimSpace(policy.Audience)
		policy.Scope = strings.TrimSpace(policy.Scope)
		policy.TenantID = strings.TrimSpace(policy.TenantID)
		policy.WorkspaceID = strings.TrimSpace(policy.WorkspaceID)
		policy.Purpose = strings.TrimSpace(policy.Purpose)
		policy.OwnerSystem = strings.TrimSpace(policy.OwnerSystem)
		policy.ResourceType = strings.TrimSpace(policy.ResourceType)
		policy.Action = strings.TrimSpace(policy.Action)
		fields := []string{policy.PolicyID, policy.Issuer, policy.Subject, policy.Audience, policy.Scope, policy.TenantID, policy.WorkspaceID, policy.Purpose, policy.OwnerSystem, policy.ResourceType, policy.Action}
		for _, field := range fields {
			if field == "" || strings.Contains(field, "*") || strings.ContainsAny(field, " \t\r\n") {
				return nil, fmt.Errorf("RECORD_HUB_COMMAND_POLICIES[%d] requires non-empty exact fields without wildcards", index)
			}
		}
		if policy.Issuer != cfg.OIDCIssuer || policy.Audience != cfg.OIDCAudience {
			return nil, fmt.Errorf("RECORD_HUB_COMMAND_POLICIES[%d] issuer/audience must match bearer OIDC configuration", index)
		}
		if policy.MaxPayloadBytes == 0 {
			policy.MaxPayloadBytes = 256 << 10
		}
		if policy.MaxPayloadBytes < 1024 || policy.MaxPayloadBytes > 256<<10 {
			return nil, fmt.Errorf("RECORD_HUB_COMMAND_POLICIES[%d].maxPayloadBytes must be between 1024 and 262144", index)
		}
		key := strings.Join(fields, "\x00")
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("RECORD_HUB_COMMAND_POLICIES[%d] duplicates an earlier policy", index)
		}
		seen[key] = struct{}{}
	}
	return policies, nil
}

func parseBindingMachinePolicies(raw string, cfg Config) ([]BindingMachinePolicyConfig, error) {
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCPrincipalKind != "service" {
		return nil, errors.New("RECORD_HUB_BINDING_MACHINE_POLICIES requires bearer OIDC with principal kind service")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var policies []BindingMachinePolicyConfig
	if err := decoder.Decode(&policies); err != nil {
		return nil, fmt.Errorf("RECORD_HUB_BINDING_MACHINE_POLICIES must be a JSON array of exact policies: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err == nil {
		return nil, errors.New("RECORD_HUB_BINDING_MACHINE_POLICIES must contain one JSON array")
	} else if !errors.Is(err, io.EOF) {
		return nil, errors.New("RECORD_HUB_BINDING_MACHINE_POLICIES must contain one valid JSON array")
	}
	if policies == nil || len(policies) == 0 {
		return nil, errors.New("RECORD_HUB_BINDING_MACHINE_POLICIES must contain at least one policy")
	}
	if len(policies) > 128 {
		return nil, errors.New("RECORD_HUB_BINDING_MACHINE_POLICIES exceeds the 128-policy limit")
	}
	seen := make(map[string]struct{}, len(policies))
	for index := range policies {
		policy := &policies[index]
		policy.Issuer = strings.TrimSpace(policy.Issuer)
		policy.Subject = strings.TrimSpace(policy.Subject)
		policy.Audience = strings.TrimSpace(policy.Audience)
		policy.Scope = strings.TrimSpace(policy.Scope)
		policy.TenantID = strings.TrimSpace(policy.TenantID)
		policy.WorkspaceID = strings.TrimSpace(policy.WorkspaceID)
		policy.Purpose = strings.TrimSpace(policy.Purpose)
		policy.ResourceSystem = strings.TrimSpace(policy.ResourceSystem)
		policy.ResourceType = strings.TrimSpace(policy.ResourceType)
		fields := []string{policy.Issuer, policy.Subject, policy.Audience, policy.Scope, policy.TenantID, policy.WorkspaceID, policy.Purpose, policy.ResourceSystem, policy.ResourceType}
		for _, field := range fields {
			if field == "" || strings.Contains(field, "*") {
				return nil, fmt.Errorf("RECORD_HUB_BINDING_MACHINE_POLICIES[%d] requires non-empty exact fields without wildcards", index)
			}
		}
		if policy.Issuer != cfg.OIDCIssuer {
			return nil, fmt.Errorf("RECORD_HUB_BINDING_MACHINE_POLICIES[%d].issuer must match RECORD_HUB_OIDC_ISSUER", index)
		}
		if policy.Audience != cfg.OIDCAudience {
			return nil, fmt.Errorf("RECORD_HUB_BINDING_MACHINE_POLICIES[%d].audience must match RECORD_HUB_OIDC_AUDIENCE", index)
		}
		key := strings.Join(fields, "\x00")
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("RECORD_HUB_BINDING_MACHINE_POLICIES[%d] duplicates an earlier policy", index)
		}
		seen[key] = struct{}{}
	}
	return policies, nil
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

func parseProjectionWorkspaceMappings(raw string) (map[string]string, error) {
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("RECORD_HUB_PROJECTION_WORKSPACE_MAP must be a JSON object of tenant to workspace IDs: %w", err)
	}
	if values == nil {
		return nil, errors.New("RECORD_HUB_PROJECTION_WORKSPACE_MAP must be a JSON object of tenant to workspace IDs")
	}
	if len(values) > 128 {
		return nil, errors.New("RECORD_HUB_PROJECTION_WORKSPACE_MAP cannot contain more than 128 tenants")
	}
	normalized := make(map[string]string, len(values))
	for tenantID, workspaceID := range values {
		tenantID = strings.TrimSpace(tenantID)
		workspaceID = strings.TrimSpace(workspaceID)
		if err := validateScopeMappingIdentifier(tenantID, "tenant"); err != nil {
			return nil, fmt.Errorf("RECORD_HUB_PROJECTION_WORKSPACE_MAP: %w", err)
		}
		if err := validateScopeMappingIdentifier(workspaceID, "workspace"); err != nil {
			return nil, fmt.Errorf("RECORD_HUB_PROJECTION_WORKSPACE_MAP: %w", err)
		}
		if _, exists := normalized[tenantID]; exists {
			return nil, fmt.Errorf("RECORD_HUB_PROJECTION_WORKSPACE_MAP: duplicate tenant ID %q after trimming", tenantID)
		}
		normalized[tenantID] = workspaceID
	}
	return normalized, nil
}

func validateScopeMappingIdentifier(value, kind string) error {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s IDs must be non-empty, at most 128 characters, and contain no whitespace", kind)
	}
	return nil
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
