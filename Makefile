.PHONY: build test lint check clean

BUILD_DIR := build
BINARY := $(BUILD_DIR)/record-hub

build:
	go build -trimpath -o $(BINARY) ./server/cmd/record-hub

test:
	go test ./...

lint:
	test -z "$$(gofmt -l server)"
	go vet ./...

check: lint test build

clean:
	rm -f $(BINARY)
