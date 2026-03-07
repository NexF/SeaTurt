.PHONY: build run clean test test-integration test-e2e build-test-image build-image build-web build-mcp release tidy lint

BINARY_NAME=containeragent-server
BUILD_DIR=bin
WEB_DIR=../seaturt-web
EMBED_DIR=cmd/server/web/dist
BUILD_SH=../build.sh

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server/

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)
	rm -rf $(EMBED_DIR)
	rm -rf mcp-bins/mcp-server-* mcp-bins/*.py
	mkdir -p $(EMBED_DIR)
	echo '<!DOCTYPE html><html><body>placeholder</body></html>' > $(EMBED_DIR)/index.html

# Build frontend and copy to embed directory
build-web:
	$(BUILD_SH) web

# Build MCP servers → mcp-bins/
build-mcp:
	$(BUILD_SH) mcp

# Full production build: frontend + backend + mcp + image
release:
	$(BUILD_SH) release

test:
	go test ./internal/... -v -race

test-integration:
	go test ./tests/integration/... -v -tags=integration -timeout 10m

test-e2e:
	go test ./tests/e2e/... -v -tags=e2e -timeout 30m

build-test-image:
	docker build -t containeragent/sandbox:test ./docker/sandbox/

build-image:
	$(BUILD_SH) image

tidy:
	go mod tidy

lint:
	golangci-lint run ./...
