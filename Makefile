# Makefile for newrelic-otel-shim

.PHONY: help test build lint vet fmt clean coverage deps security check-mod-tidy

# Default target
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Development tasks
deps: ## Download dependencies
	go mod download
	go mod verify

test: ## Run tests
	go test -v -race -coverprofile=coverage.out ./...

test-short: ## Run tests without race detection
	go test -v ./...

coverage: test ## Generate coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

build: ## Build the project
	go build -v ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format code
	go fmt ./...
	goimports -w .

lint: ## Run golangci-lint
	golangci-lint run

security: ## Run security scan
	gosec ./...

check-mod-tidy: ## Check if go mod tidy is needed
	@go mod tidy
	@git diff --exit-code -- go.mod go.sum || (echo "go mod tidy resulted in changes. Please run 'go mod tidy'." && exit 1)

# CI simulation
ci: deps vet lint test build check-mod-tidy security ## Run all CI checks locally

integration-test: ## Run drop-in replacement integration test
	@echo "Running drop-in replacement integration test..."
	cd integration-test && chmod +x test-drop-in-replacement.sh && ./test-drop-in-replacement.sh

# Clean up
clean: ## Clean build artifacts
	go clean ./...
	rm -f coverage.out coverage.html

# Install development tools
tools: ## Install development tools
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest
	go install golang.org/x/tools/cmd/goimports@latest
