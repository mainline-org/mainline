.PHONY: build test bench lint clean bootstrap

# Build the mainline binary
build:
	go build -o mainline .

# Run all tests with race detection
test:
	go test -race -count=1 ./...

# Run tests verbose
test-verbose:
	go test -v -race -count=1 ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./internal/core/ ./internal/engine/

# Run linter
lint:
	go vet ./...
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

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

# Full CI pipeline
ci: lint test bench build
