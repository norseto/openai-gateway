# OpenAI Gateway Makefile

.PHONY: all build run test clean help set-version get-version build-installer tag-release

OUT_BIN_DIR=bin
BINARY_NAME=openai-gateway
BINARY_PATH=$(OUT_BIN_DIR)/$(BINARY_NAME)

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	mkdir -p $(OUT_BIN_DIR)
	go build -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)

run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BINARY_PATH) --open-webui-url=http://localhost:3000 --port=8080

test:
	@echo "Running tests..."
	go test ./...

clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_PATH)

help:
	@echo "Makefile commands:"
	@echo "  make all   - Build the application"
	@echo "  make build - Build the binary"
	@echo "  make run   - Build and run the application"
	@echo "  make test  - Run tests"
	@echo "  make clean - Clean up build artifacts"
	@echo "  make help  - Show this help message"

# Version management targets
.PHONY: set-version
set-version:
	@echo "Setting version..."
	@bash hack/set-version.sh $(VERSION)

.PHONY: get-version
get-version:
	@echo "Getting version..."
	@bash hack/get-version.sh

.PHONY: build-installer
build-installer:
	@echo "Building installer with image $(IMG)..."
	@kustomize build config/openai-gateway > dist/openai-gateway.yaml
	echo "Build completed for $(IMG)"

.PHONY: tag-release
tag-release:
	@echo "Tagging release..."
	@bash hack/tag-release.sh
	echo "Release tagged"
