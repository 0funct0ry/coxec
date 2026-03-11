.PHONY: all build test lint clean check help

APP_NAME := coxec
BUILD_DIR := bin
CMD_DIR := .

# Default target
all: lint test build

# Build the binary
build:
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags "-s -w" -o $(BUILD_DIR)/$(APP_NAME) $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME)"

# Run tests
test:
	@echo "Running tests..."
	@go test -v -race ./...

# Run linters
lint:
	@echo "Running golangci-lint..."
	@golangci-lint run ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)

# Help
help:
	@echo "Makefile commands:"
	@echo "  make all    - Lint, test, and build"
	@echo "  make build  - Build the application"
	@echo "  make test   - Run tests"
	@echo "  make lint   - Run golangci-lint"
	@echo "  make clean  - Remove build artifacts"
