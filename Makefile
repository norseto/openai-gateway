# OpenAI Gateway Makefile

.PHONY: all build run test clean help

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
