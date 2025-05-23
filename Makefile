# OpenAI Gateway Makefile

.PHONY: all build run test test-coverage clean help set-version get-version build-installer tag-release

OUT_BIN_DIR=bin
BINARY_NAME=openai-gateway
BINARY_PATH=$(OUT_BIN_DIR)/$(BINARY_NAME)
PLATFORMS=linux/amd64,linux/arm64

IMG=norseto/openai-gateway
MODULE_PACKAGE=github.com/norseto/openai-gateway
GITSHA := $(shell git describe --always)
LDFLAGS := -ldflags=all=

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	mkdir -p $(OUT_BIN_DIR)
	go build $(LDFLAGS)"-X $(MODULE_PACKAGE).GitVersion=$(GITSHA)" -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)

run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BINARY_PATH) serve --open-webui-url=http://localhost:3000 --port=8080

test:
	@echo "Running tests..."
	go test ./... -coverprofile=coverage.out

clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_PATH)

help:
	@echo "Makefile commands:"
	@echo "  make all           - Build the application"
	@echo "  make build         - Build the binary"
	@echo "  make run           - Build and run the application"
	@echo "  make test          - Run tests"
	@echo "  make clean         - Clean up build artifacts"
	@echo "  make test-coverage - Run tests and generate HTML coverage report"
	@echo "  make clean         - Clean up build artifacts"
	@echo "  make docker-buildx - Build multi-arch Docker image (amd64, arm64) using buildx"
	@echo "  make help          - Show this help message"

.PHONY: test-coverage
test-coverage:
	@echo "Running tests and generating coverage report..."
	@go test -coverprofile=coverage.out ./...
	@echo "Generating HTML coverage report (coverage.html)..."
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

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

.PHONY: docker-buildx
docker-buildx:
	docker buildx build --platform $(PLATFORMS) \
		-t $(IMG) \
		--build-arg MODULE_PACKAGE=$(MODULE_PACKAGE) \
		--build-arg GITVERSION=$(GITSHA) \
		--push \
		-f Dockerfile .
.PHONY: docker-buildx

docker-build:
	docker build \
		-t $(IMG) \
		--build-arg MODULE_PACKAGE=$(MODULE_PACKAGE) \
		--build-arg GITVERSION=$(GITSHA) \
		-f Dockerfile .
