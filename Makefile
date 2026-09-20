.PHONY: build test lint web-check openapi-lint generate-clients dependency-scan sbom secret-scan mongo-up mongo-smoke mongo-down schema-persistence-smoke records-persistence-smoke projection-persistence-smoke nats-up nats-init nats-smoke nats-permissions-smoke nats-down dex-env dex-up dex-smoke m5-runtime-smoke m7-native-restart m7-native-nats-recovery m8-happy-path m8-failure-path m8-local-smoke m8-native-smoke p2-workload-identity-smoke p2-isolated-core p2-engine-foundation p2-mapping-recovery p2-rebuild-cas p2-rebuild-replay p2-slo-baseline p2-realtime-feed p2-command-contract p2-command-result p3-contract-gate p3-fluxion-command-live p3-fluxion-command-faults p3-fixtures p3-four-owner-topology p3-workflow-e2e p3-nats-outage-recovery p3-process-restart-ack-loss p3-credential-rotation p3-backup-restore p3-capacity-baseline p3-upgrade-rollback p3-phase3-report p4-baseline p4-contract-inventory p4-evidence-contract p4-fixtures p4-batch1 p4-batch2 p4-batch3 p4-batch4 p4-release-candidate p4-batch5 p5-001 p5-002 p5-100 p5-101 p5-102 p5-200 p5-201 p5-202 p5-203 p5-300 p5-301 p5-302 p5-303 p5-400 p5-401 p5-402 p5-403 p5-500 p5-501 p5-topology p5-topology-live p6-001 p6-002 p6-100 p6-101 p6-102 p6-103 p6-200 p6-201 p6-202 p6-203 p6-300 p6-301 p6-302 p6-400 p6-401 p6-402 p6-403 p6-500 p6-501 p6-502 p6-503 check ci clean

BUILD_DIR := build
BINARY := $(BUILD_DIR)/record-hub

build:
	go build -trimpath -o $(BINARY) ./server/cmd/record-hub

test:
	go test ./...

lint:
	test -z "$$(gofmt -l contracts server tools)"
	go vet ./...

web-check:
	cd web && npm ci && npm run typecheck && npm test && npm run build

openapi-lint:
	go run ./tools/openapi-lint api/openapi.yaml

generate-clients:
	./scripts/generate-clients.sh

dependency-scan:
	go mod verify
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

sbom: build
	./scripts/generate-sbom.sh

secret-scan:
	./scripts/scan-secrets.sh

mongo-up:
	docker compose -f deploy/local/mongodb/compose.yaml up -d --wait mongodb mongodb-init

mongo-smoke:
	go run ./tools/mongo-smoke

schema-persistence-smoke:
	go test ./server/internal/modules/schema -run TestMongo -count=1 -v

records-persistence-smoke:
	go test ./server/internal/modules/records -run TestMongo -count=1 -v

projection-persistence-smoke:
	go test ./server/internal/modules/projection -run TestMongo -count=1 -v

mongo-down:
	docker compose -f deploy/local/mongodb/compose.yaml down

nats-up:
	docker compose -f deploy/local/nats/compose.yaml up -d --wait nats

nats-init:
	go run ./tools/nats-init

nats-smoke:
	go run ./tools/nats-init --smoke

nats-permissions-smoke:
	go run ./tools/nats-permissions-smoke

nats-down:
	docker compose -f deploy/local/nats/compose.yaml down

m5-runtime-smoke:
	./scripts/verify-m5-runtime.sh

m5-supervised-live:
	./scripts/verify-m5-supervised-live.sh

m7-native-restart:
	./scripts/verify-m7-native-restart.sh

m7-native-nats-recovery:
	./scripts/verify-m7-native-nats-recovery.sh

dex-env:
	./deploy/local/dex/initialize-env.sh

dex-up:
	docker compose --env-file deploy/local/dex/.env.local -f deploy/local/dex/compose.yaml up -d --wait dex

dex-smoke:
	set -a; . deploy/local/dex/.env.local; set +a; go run ./tools/dex-smoke

dex-down:
	docker compose --env-file deploy/local/dex/.env.local -f deploy/local/dex/compose.yaml down

m8-happy-path:
	./scripts/verify-m8-happy-path.sh

m8-failure-path:
	./scripts/verify-m8-failure-path.sh

m8-local-smoke:
	./scripts/verify-m8-local.sh

m8-native-smoke:
	RECORD_HUB_M8_LOCAL_LIVE=1 RECORD_HUB_M8_RUNTIME=native ./scripts/verify-m8-local.sh

p2-workload-identity-smoke:
	./scripts/verify-p2-workload-identity.sh

p2-isolated-core:
	./scripts/verify-p2-isolated-core.sh

p2-engine-foundation:
	./scripts/verify-p2-engine-foundation.sh

p2-mapping-recovery:
	RECORD_HUB_MONGODB_URI="$${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}" \
	RECORD_HUB_NATS_URL="$${RECORD_HUB_NATS_URL:-nats://127.0.0.1:4222}" \
	go test ./server/internal/modules/projection -run TestPublishedMappingJetStreamRecovery -count=1 -v

p2-rebuild-cas:
	RECORD_HUB_MONGODB_URI="$${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}" \
	go test ./server/internal/modules/projection -run 'TestMongoRebuildRepositoryCASAndReceipt|TestProjectionRebuild' -count=1 -v

p2-rebuild-replay:
	RECORD_HUB_MONGODB_URI="$${RECORD_HUB_MONGODB_URI:-mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}" \
	go test ./server/internal/modules/projection -run TestMongoProjectionReplayStagingReadPointerAndLiveContinuation -count=1 -v

p2-slo-baseline:
	go test ./server/internal/observability ./server/internal/config ./server/internal/modules/records ./server/internal/modules/projection -count=1 -v

p2-realtime-feed:
	go test ./server/internal/modules/records ./server/internal/web -run 'TestRecordFeed|TestFeedHTTP' -count=1 -v

p2-command-contract:
	go test ./server/internal/modules/commands ./server/internal/config -run 'TestCommand|TestLoadCommand' -count=1 -v

p2-command-result:
	go test ./server/internal/modules/commands -run 'TestCommandApplyResult|TestInbox' -count=1 -v

p3-contract-gate:
	./scripts/verify-p3-contract-mirrors.sh
	./scripts/verify-p3-approval-contract-mirrors.sh

p3-fluxion-command-live:
	./scripts/verify-p3-fluxion-command-live.sh

p3-fluxion-command-faults:
	RECORD_HUB_P3_FAULT_MATRIX=1 ./scripts/verify-p3-fluxion-command-live.sh

p3-fixtures:
	./scripts/bootstrap-p3-fixtures.sh

p3-four-owner-topology:
	./scripts/verify-p3-four-owner-topology.sh

p3-workflow-e2e:
	RECORD_HUB_P3_WORKFLOW_E2E_LIVE=1 ./scripts/verify-p3-workflow-e2e.sh

p3-nats-outage-recovery:
	RECORD_HUB_P3_NATS_RECOVERY_LIVE=1 ./scripts/verify-p3-nats-outage-recovery.sh

p3-process-restart-ack-loss:
	RECORD_HUB_P3_RESTART_ACK_LIVE=1 ./scripts/verify-p3-process-restart-ack-loss.sh

p3-credential-rotation:
	RECORD_HUB_P3_ROTATION_LIVE=1 ./scripts/verify-p3-credential-rotation.sh

p3-backup-restore:
	RECORD_HUB_P3_BACKUP_LIVE=1 ./scripts/verify-p3-backup-restore.sh

p3-capacity-baseline:
	RECORD_HUB_P3_CAPACITY_LIVE=1 ./scripts/verify-p3-capacity-baseline.sh

p3-upgrade-rollback:
	RECORD_HUB_P3_UPGRADE_LIVE=1 ./scripts/verify-p3-upgrade-rollback.sh

p3-phase3-report:
	./scripts/verify-p3-phase3-report.sh

p4-baseline:
	RECORD_HUB_P4_BASELINE_BUILD=1 ./scripts/bootstrap-p4-baseline.sh

p4-batch4:
	./scripts/verify-p4-batch4.sh

p4-release-candidate:
	./scripts/bootstrap-p4-release-candidate.sh

p4-batch5:
	./scripts/verify-p4-batch5.sh

p5-001:
	RECORD_HUB_P5_BASELINE_BUILD=1 ./scripts/bootstrap-p5-baseline.sh

p5-002:
	./scripts/verify-p5-002-live-gap.sh

p6-001:
	./scripts/bootstrap-p6-001-inventory.sh

p6-002:
	./scripts/verify-p6-002-evidence-contract.sh

p6-100:
	./scripts/verify-p6-100-schema-registry.sh

p6-101:
	./scripts/verify-p6-101-tag-contract.sh

p6-102:
	./scripts/verify-p6-102-relation-contract.sh

p6-103:
	./scripts/verify-p6-103-view-contract.sh

p6-200:
	./scripts/verify-p6-200-connector-registry.sh

p6-201:
	./scripts/verify-p6-201-sdk-parity.sh

p6-202:
	./scripts/verify-p6-202-workload-identity.sh

p6-203:
	./scripts/verify-p6-203-fault-matrix.sh

p6-300:
	./scripts/verify-p6-300-approver-projection.sh

p6-301:
	./scripts/verify-p6-301-fluxion-approval.sh

p6-302:
	./scripts/verify-p6-302-bids-approval.sh

p6-303:
	./scripts/verify-p6-303-settlement-association.sh

p6-400:
	./scripts/verify-p6-400-temporal-binding.sh

p6-401:
	./scripts/verify-p6-401-conductor-binding.sh

p6-402:
	./scripts/verify-p6-402-nats-event.sh

p6-403:
	./scripts/verify-p6-403-event-table.sh

p6-500:
	./scripts/verify-p6-500-control-plane.sh

p6-501:
	./scripts/verify-p6-501-connector-onboarding.sh

p6-502:
	./scripts/verify-p6-502-observation-dashboard.sh

p6-503:
	./scripts/verify-p6-503-operator-runbook.sh

p5-100:
	./scripts/verify-p5-100-identity.sh

p5-101:
	./scripts/verify-p5-101-scope.sh

p5-102:
	./scripts/verify-p5-102-rbac.sh

p5-200:
	./scripts/verify-p5-200-mongo.sh

p5-201:
	./scripts/verify-p5-201-nats.sh

p5-202:
	./scripts/verify-p5-202-capacity.sh

p5-203:
	./scripts/verify-p5-203-rolling-rollback.sh

p5-300:
	./scripts/verify-p5-300-connectors.sh

p5-301:
	./scripts/verify-p5-301-settlement.sh

p5-302:
	./scripts/verify-p5-302-settlement-projection.sh

p5-303:
	./scripts/verify-p5-303-approval-connectors.sh

p5-400:
	./scripts/verify-p5-400-security.sh

p5-401:
	./scripts/verify-p5-401-canary.sh

p5-402:
	./scripts/verify-p5-402-ga-report.sh

p5-403:
	./scripts/verify-p5-403-legacy-review.sh

p5-500:
	./scripts/verify-p5-500-ga-candidate.sh

p5-501:
	./scripts/verify-p5-501-rollout.sh

p5-topology:
	./scripts/verify-p5-native-topology.sh

p5-topology-live:
	RECORD_HUB_P5_TOPOLOGY_LIVE=1 ./scripts/verify-p5-native-topology.sh

p4-contract-inventory:
	./scripts/verify-p4-contract-inventory.sh

p4-evidence-contract:
	./scripts/verify-p4-evidence-manifest.sh

p4-fixtures:
	./scripts/bootstrap-p4-fixtures.sh

p4-batch1:
	./scripts/verify-p4-batch1.sh

p4-batch2:
	./scripts/verify-p4-batch2.sh

p4-batch3:
	./scripts/verify-p4-batch3.sh

check: lint web-check openapi-lint test build

ci: check dependency-scan sbom secret-scan

clean:
	rm -f $(BINARY)
