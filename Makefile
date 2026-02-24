BINARY := airgap
MODULE := github.com/BadgerOps/airgap
VERSION ?= $(shell python3 scripts/validate_versions.py --latest-version 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)
GOFLAGS := -trimpath

.PHONY: all build clean test lint fmt vet container run help version-check

all: build

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/airgap

clean:
	rm -rf bin/ dist/

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

version-check:
	python3 scripts/validate_versions.py

container:
	podman build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(BINARY):$(VERSION) -f Containerfile .

run: build
	./bin/$(BINARY) serve

help:
	@echo "Targets:"
	@echo "  build      - Build the binary"
	@echo "  clean      - Remove build artifacts"
	@echo "  test       - Run tests with race detector"
	@echo "  lint       - Run golangci-lint"
	@echo "  fmt        - Format Go code"
	@echo "  vet        - Run go vet"
	@echo "  version-check - Validate CHANGELOG format/versioning"
	@echo "  container  - Build container image"
	@echo "  run        - Build and run the server"
