.PHONY: build run clean test test-integration test-e2e build-test-image build-image build-web release

BINARY_NAME=containeragent-server
BUILD_DIR=bin
WEB_DIR=../seaturt-web
EMBED_DIR=cmd/server/web/dist

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server/

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	echo '<!DOCTYPE html><html><body>placeholder</body></html>' > $(EMBED_DIR)/index.html

# Build frontend and copy to embed directory
build-web:
	cd $(WEB_DIR) && npm run build
	rm -rf $(EMBED_DIR)
	cp -r $(WEB_DIR)/dist $(EMBED_DIR)

# Full production build: frontend + backend → single binary
release: build-web
	go build -o $(BUILD_DIR)/seaturt ./cmd/server/
	@echo "✅ Production binary: $(BUILD_DIR)/seaturt"

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
