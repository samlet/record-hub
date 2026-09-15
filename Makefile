.PHONY: build test lint openapi-lint generate-clients dependency-scan sbom secret-scan mongo-up mongo-smoke mongo-down check ci clean

BUILD_DIR := build
BINARY := $(BUILD_DIR)/record-hub

build:
	go build -trimpath -o $(BINARY) ./server/cmd/record-hub

test:
	go test ./...

lint:
	test -z "$$(gofmt -l contracts server tools)"
	go vet ./...

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
	docker compose -f deploy/local/compose.yaml up -d --wait mongodb mongodb-init

mongo-smoke:
	go run ./tools/mongo-smoke

mongo-down:
	docker compose -f deploy/local/compose.yaml down

check: lint openapi-lint test build

ci: check dependency-scan sbom secret-scan

clean:
	rm -f $(BINARY)
