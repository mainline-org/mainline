.PHONY: build test bench lint clean bootstrap

GOLANGCI_LINT_VERSION ?= v1.62.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Build the mainline binary
build:
	go build -o mainline .

# Run all tests with race detection. -short keeps the rapid PBTs at 20
# samples per property; CI uses the full 100 via the `ci` target.
test:
	go test -race -count=1 -short ./...

# Run tests verbose
test-verbose:
	go test -v -race -count=1 -short ./...

# Run tests with full rapid PBT coverage (100 samples per property).
test-pbt:
	go test -race -count=1 ./...

# Inner-loop test: skip rapid PBT files via the `!quick` build tag and the
# package-level race detector for fastest feedback (~5s).
quick-test:
	go test -count=1 -tags quick ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./internal/core/ ./internal/engine/

# Run the same pinned golangci-lint version as GitHub CI, then vet.
lint:
	$(GOLANGCI_LINT) run --timeout=5m
	go vet ./...

# Clean build artifacts
clean:
	rm -f mainline
	rm -rf .ml-cache

# Install the binary
install:
	go install .

# Bootstrap: use mainline to manage itself
bootstrap: build
	./mainline init --actor-name "mainline-dev"
	@echo "Mainline bootstrapped! Use './mainline status' to check."

# Self-test: run tests then bootstrap
self-test: test build bootstrap
	./mainline status
	./mainline start --goal "Initial mainline implementation"
	./mainline append "Core domain types, engine, CLI, tests"
	@echo "Self-test complete. Intent started for self-management."

# Full CI pipeline (full rapid PBT coverage)
ci: lint test-pbt bench build
