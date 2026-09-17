package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/samlet/record-hub/server/internal/config"
	"github.com/samlet/record-hub/server/internal/health"
	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/binding"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/projection"
	"github.com/samlet/record-hub/server/internal/modules/records"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"github.com/samlet/record-hub/server/internal/observability"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type runtimeDependencies struct {
	mongoClient *mongo.Client
	natsClient  *projection.Client
	checks      map[health.Dependency]health.Checker
	records     http.Handler
	schema      http.Handler
	catalog     http.Handler
	binding     http.Handler
	operations  http.Handler
	rebuild     http.Handler
	workers     []Service
	webVerifier identity.TokenVerifier
	closer      Service
}

func newRuntime(cfg config.Config, metrics *observability.Registry, logger *slog.Logger) (*runtimeDependencies, error) {
	deps := &runtimeDependencies{checks: map[health.Dependency]health.Checker{
		health.MongoDB: health.Pending(),
		health.NATS:    health.Pending(),
		health.Dex:     health.CheckFunc(func(context.Context) error { return nil }),
	}}
	if cfg.Web.Enabled {
		deps.checks[health.Dex] = oidcDiscoveryChecker(cfg.Web.Issuer, cfg.Web.AllowInsecureEndpoints)
	}
	if strings.TrimSpace(cfg.OIDCIssuer) != "" {
		kind := identity.PrincipalUser
		if cfg.OIDCPrincipalKind == string(identity.PrincipalService) {
			kind = identity.PrincipalService
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		verifier, err := identity.NewOIDCVerifier(ctx, identity.OIDCVerifierConfig{Issuer: cfg.OIDCIssuer, Audience: cfg.OIDCAudience, PrincipalKind: kind, AllowInsecureIssuer: cfg.OIDCAllowInsecureIssuer})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("configure bearer OIDC verifier: %w", err)
		}
		deps.webVerifier = verifier
		deps.checks[health.Dex] = oidcDiscoveryChecker(cfg.OIDCIssuer, cfg.OIDCAllowInsecureIssuer)
	}

	if strings.TrimSpace(cfg.MongoURI) == "" {
		if (cfg.Mode == config.ModeWorker || cfg.Mode == config.ModeAll) && strings.TrimSpace(cfg.NATSURL) != "" {
			return nil, errors.New("RECORD_HUB_MONGODB_URI is required when NATS projection workers are enabled")
		}
		return deps, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.MongoURI).SetServerSelectionTimeout(5 * time.Second))
	if err == nil {
		err = client.Ping(ctx, nil)
	}
	cancel()
	if err != nil {
		if client != nil {
			_ = client.Disconnect(context.Background())
		}
		return nil, fmt.Errorf("connect MongoDB: %w", err)
	}
	deps.mongoClient = client
	database := client.Database(cfg.MongoDatabase)
	if err := ensureMongoIndexes(database); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	deps.checks[health.MongoDB] = health.CheckFunc(func(ctx context.Context) error { return client.Ping(ctx, nil) })
	deps.closer = runtimeCloser{mongoClient: client, timeout: cfg.ShutdownTimeout}

	membership := identity.NewMongoMembershipReader(database)
	authorizer := identity.NewAuthorizer(membership)
	recordRepo := records.NewMongoRepository(database)
	schemaRepo := schema.NewMongoRepository(database)
	auditWriter := audit.NewMongoWriter(database)
	catalogRepo := projection.NewMongoCatalogRepository(database)
	rebuildRepo := projection.NewMongoRebuildRepository(database)
	rebuildReceipts := projection.NewMongoRebuildReceiptStore(database)
	readPointers := projection.NewMongoProjectionReadPointerRepository(database)
	eventArchive := projection.NewMongoProjectionEventArchive(database)
	catalogReceipts := projection.NewMongoCatalogReceiptStore(database)
	schemaReceipts := schema.NewMongoReceiptStore(database)
	migrationReceipts := schema.NewMongoMigrationPlanReceiptStore(database)
	recordReceipts := records.NewMongoRecordReceiptStore(database)
	schemaService := schema.NewService(schemaRepo, authorizer, schemaReceipts, auditWriter)
	migrationService := schema.NewMigrationService(schemaRepo, schemaRepo, authorizer, migrationReceipts, auditWriter)
	catalogService := projection.NewCatalogService(catalogRepo, catalogRepo, schemaRepo, authorizer, catalogReceipts, auditWriter)
	recordService := records.NewRecordService(recordRepo, recordRepo, schemaRepo, authorizer, recordRepo, recordReceipts, auditWriter).WithViewRepository(recordRepo).WithIndexRepository(recordRepo)
	snapshotStore := binding.NewMongoSnapshotStore(database)
	var machineAuthorizer binding.PolicyAuthorizer
	if len(cfg.BindingMachinePolicies) > 0 {
		policies := make([]binding.MachinePolicy, 0, len(cfg.BindingMachinePolicies))
		for _, policy := range cfg.BindingMachinePolicies {
			policies = append(policies, binding.MachinePolicy{
				Identity:       identity.IdentityKey{Issuer: policy.Issuer, Subject: policy.Subject},
				Audience:       policy.Audience,
				Scope:          policy.Scope,
				TenantID:       policy.TenantID,
				WorkspaceID:    policy.WorkspaceID,
				Purpose:        policy.Purpose,
				ResourceSystem: policy.ResourceSystem,
				ResourceType:   policy.ResourceType,
			})
		}
		machineAuthorizer = binding.NewStaticMachinePolicyAuthorizer(policies...)
	}
	bindingService := binding.NewService(binding.NewMongoRecordReader(recordRepo), snapshotStore, authorizer, machineAuthorizer)
	operationsService := projection.NewOperationsService(projection.NewMongoProjectionRepository(database), authorizer).WithMetrics(metrics)
	generations := projection.NewMappingGenerationRegistry()
	generationBuilder := projection.NewMappingGenerationBuilder(catalogRepo, schemaRepo)
	rebuildService := projection.NewProjectionRebuildService(rebuildRepo, generationBuilder, generations, authorizer, rebuildReceipts, auditWriter).WithReplayDependencies(eventArchive, readPointers)
	deps.records = records.NewHTTPHandler(recordService)
	deps.schema = schema.NewHTTPHandler(schemaService, migrationService)
	deps.catalog = projection.NewCatalogHTTPHandler(catalogService)
	deps.binding = binding.NewHTTPHandler(bindingService)
	deps.operations = projection.NewOperationsHTTPHandler(operationsService)
	deps.rebuild = projection.NewProjectionRebuildHTTPHandler(rebuildService)

	if strings.TrimSpace(cfg.NATSURL) == "" {
		if cfg.Mode == config.ModeWorker || cfg.Mode == config.ModeAll {
			_ = client.Disconnect(context.Background())
			return nil, errors.New("RECORD_HUB_NATS_URL is required in worker/all mode when MongoDB is configured")
		}
		return deps, nil
	}
	natsClient, err := projection.Connect(cfg.NATSURL, nats.Name("record-hub-runtime"))
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	deps.natsClient = natsClient
	deps.checks[health.NATS] = health.CheckFunc(natsClient.Check)
	generationRefresher := projection.NewMappingGenerationRefresher(generationBuilder, generations, 5*time.Second, logger)
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 8*time.Second)
	_, err = generationRefresher.Refresh(refreshCtx)
	refreshCancel()
	if err != nil {
		natsClient.Close()
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("load projection mapping generation: %w", err)
	}
	registry := projection.NewHandlerRegistry()
	if err := projection.RegisterSummaryHandlers(registry); err != nil {
		natsClient.Close()
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("register projection handlers: %w", err)
	}
	projectionRepo := projection.NewMongoProjectionRepository(database)
	projector, err := projection.NewSummaryProjector(registry, projection.NewMongoInboxRepository(database), projectionRepo, cfg.ProjectionWorkspaceID)
	if err != nil {
		natsClient.Close()
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("configure summary projector: %w", err)
	}
	projector.WithWorkspaceMappings(cfg.ProjectionWorkspaceMappings).WithMappingGenerations(generations).WithEventArchive(eventArchive)
	dlq, err := projection.NewNATSDeadLetterPublisher(natsClient.Publisher(), "dlq.record-hub")
	if err != nil {
		natsClient.Close()
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("configure projection DLQ: %w", err)
	}
	deps.workers = append(deps.workers, generationRefresher)
	deps.workers = append(deps.workers, projection.NewProjectionRebuildWorker(rebuildService, logger))
	for _, durable := range []string{projection.ApproverProjectionConsumer, projection.FluxionProjectionConsumer, projection.BidsProjectionConsumer} {
		runner, runnerErr := projection.NewPullRunner(natsClient, projection.PullRunnerConfig{Stream: "DOMAIN_EVENTS", Durable: durable, BatchSize: 16, FetchTimeout: time.Second}, projector.HandleMessage)
		if runnerErr != nil {
			natsClient.Close()
			_ = client.Disconnect(context.Background())
			return nil, fmt.Errorf("configure projection consumer %s: %w", durable, runnerErr)
		}
		deps.workers = append(deps.workers, runner.WithDeadLetterPublisher(dlq).WithMetrics(metrics).WithLogger(logger))
	}
	deps.closer = runtimeCloser{mongoClient: client, natsClient: natsClient, timeout: cfg.ShutdownTimeout}
	return deps, nil
}

func ensureMongoIndexes(database *mongo.Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := identity.NewMongoMembershipReader(database).EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("ensure membership indexes: %w", err)
	}
	if err := schema.NewMongoRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	recordRepo := records.NewMongoRepository(database)
	if err := recordRepo.EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := schema.NewMongoReceiptStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := schema.NewMongoMigrationPlanReceiptStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := records.NewMongoRecordReceiptStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := audit.NewMongoWriter(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := binding.NewMongoSnapshotStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoInboxRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoProjectionRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoCatalogRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoCatalogReceiptStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoRebuildRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoRebuildReceiptStore(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoProjectionReadPointerRepository(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	if err := projection.NewMongoProjectionEventArchive(database).EnsureIndexes(ctx); err != nil {
		return err
	}
	return nil
}

type runtimeCloser struct {
	mongoClient *mongo.Client
	natsClient  *projection.Client
	timeout     time.Duration
}

func (runtimeCloser) Name() string { return "runtime-dependencies" }

func (closer runtimeCloser) Run(ctx context.Context) error {
	<-ctx.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), closer.timeout)
	defer cancel()
	var errs []error
	if closer.natsClient != nil {
		if err := closer.natsClient.Drain(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
			errs = append(errs, err)
		}
	}
	if closer.mongoClient != nil {
		if err := closer.mongoClient.Disconnect(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func oidcDiscoveryChecker(issuer string, allowInsecure bool) health.Checker {
	parsed, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && (!allowInsecure || parsed.Scheme != "http")) {
		return health.Pending()
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + "/.well-known/openid-configuration"
	return health.CheckFunc(func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode)
		}
		return nil
	})
}
