.PHONY: build run clean test test-integration test-e2e build-test-image build-image

BINARY_NAME=containeragent-server
BUILD_DIR=bin

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server/

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./internal/... -v -race

test-integration:
	go test ./tests/integration/... -v -tags=integration -timeout 10m

test-e2e:
	go test ./tests/e2e/... -v -tags=e2e -timeout 30m

build-test-image:
	docker build -t containeragent/sandbox:test ./docker/sandbox/

build-image:
	docker build -t seaturt/sandbox:latest ./docker/sandbox/

tidy:
	go mod tidy

lint:
	golangci-lint run ./...
