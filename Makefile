.PHONY: build test lint openapi-lint generate-clients check clean

BUILD_DIR := build
BINARY := $(BUILD_DIR)/record-hub

build:
	go build -trimpath -o $(BINARY) ./server/cmd/record-hub

test:
	go test ./...

lint:
	test -z "$$(gofmt -l server tools)"
	go vet ./...

openapi-lint:
	go run ./tools/openapi-lint api/openapi.yaml

generate-clients:
	./scripts/generate-clients.sh

check: lint openapi-lint test build

clean:
	rm -f $(BINARY)
