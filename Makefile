# bunkerctl Makefile

.PHONY: build test test-integration vet tidy

# Build the bunkerctl binary into ./bin/bunkerctl.
build:
	mkdir -p bin
	go build -o bin/bunkerctl .

# Run the default unit-test suite (integration tests are excluded by the
# `integration` build tag).
test:
	go test ./...

# Run the integration tests against a real Podman engine. Skips automatically
# when podman is not on PATH. Requires: go test -tags=integration ./cmd/...
test-integration:
	go test -tags=integration ./cmd/...

# Run go vet across all packages.
vet:
	go vet ./...

# Tidy module dependencies.
tidy:
	go mod tidy