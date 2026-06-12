.PHONY: build test test-verbose test-pbt quick-test bench lint hygiene clean install bootstrap self-test ci ci-quick ci-full ci-release

GOLANGCI_LINT_VERSION ?= v1.62.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Build the mainline binary
build:
	go build -o mainline .

# Run all tests with race detection. -short keeps the rapid PBTs at 20
# samples per property; full 100-sample PBT lives behind `test-pbt`.
test:
	go test -race -count=1 -short ./...

# Run tests verbose
test-verbose:
	go test -v -race -count=1 -short ./...

# Run the deep rapid PBT gate with race detection. The engine package shells
# out to git heavily, so keep the check count explicit and within the 30m
# GitHub Actions budget instead of relying on rapid's package-wide default.
# rapid flags must only be passed to packages that import rapid.
test-pbt:
	go test -race -count=1 -timeout=30m $$(go list ./... | grep -v '/internal/engine$$')
	go test -race -count=1 -timeout=30m ./internal/engine -rapid.checks=20

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

# Check open-source repo hygiene: no local caches/configs, hub exports, or
# high-signal credential patterns in tracked files.
hygiene:
	scripts/hygiene-check.sh

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

# Required PR gate: fast feedback, no full rapid PBT.
ci-quick: hygiene lint quick-test build

# Non-blocking deep check: full rapid PBT coverage with race detection.
ci-full: test-pbt

# Release gate: full correctness checks before publishing artifacts.
ci-release: hygiene lint test-pbt build

# Default CI target matches the required PR gate.
ci: ci-quick
