.PHONY: build test lint web-check openapi-lint generate-clients dependency-scan sbom secret-scan mongo-up mongo-smoke mongo-down schema-persistence-smoke records-persistence-smoke projection-persistence-smoke nats-up nats-init nats-smoke nats-permissions-smoke nats-down dex-env dex-up dex-smoke dex-down m5-runtime-smoke m5-supervised-live m7-native-restart m7-native-nats-recovery m8-happy-path m8-failure-path m8-local-smoke m8-native-smoke p2-workload-identity-smoke p2-isolated-core p2-engine-foundation p2-mapping-recovery p2-rebuild-cas p2-rebuild-replay p2-slo-baseline p2-realtime-feed p2-command-contract p2-command-result p3-contract-gate check ci clean

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

check: lint web-check openapi-lint test build

ci: check dependency-scan sbom secret-scan

clean:
	rm -f $(BINARY)
