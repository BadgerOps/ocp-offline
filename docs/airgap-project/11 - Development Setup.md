# Development Setup

#airgap #nix #dev #testing

Back to [[00 - Project Index]] | Related: [[09 - Technical Decisions]]

## Prerequisites

- [Nix](https://nixos.org/download.html) with flakes enabled
- Git

## Getting Started

```bash
# Enter the dev shell (installs Go, tools, everything)
nix develop

# First time: resolve all Go dependencies
go mod tidy

# Build
go build ./cmd/airgap

# Test
go test ./...

# Test verbose
go test -v ./...

# Test with coverage
go test -cover ./...
```

## Nix Flake

The project uses a Nix flake (`flake.nix`) for reproducible development environments. It provides:

### `nix develop` — Dev Shell

Everything you need to build, test, lint, and run the project:

- **Go 1.23** — compiler and toolchain
- **gopls** — Go language server
- **gotools** — goimports, gorename, etc.
- **staticcheck** — static analysis
- **delve** — debugger
- **golangci-lint** — linter aggregator
- **podman** — container builds
- **skopeo** — container image inspection
- **zstd** — compression for export engine
- **createrepo_c** — RPM repo metadata generation
- **jq, yq, curl, sqlite** — general utilities

### `nix build` — Build Binary

Produces the `airgap` binary in `./result/bin/airgap`.

### `nix build .#container` — Container Image

Builds a minimal OCI container image with:
- The `airgap` binary
- TLS certificates
- zstd, createrepo_c, sqlite, coreutils, bash
- Exposes port 8080
- Volumes for `/var/lib/airgap` and `/mnt/transfer-disk`

## Makefile Targets

```bash
make build          # go build ./cmd/airgap
make test           # go test ./...
make test-verbose   # go test -v ./...
make test-coverage  # go test -coverprofile=coverage.out ./...
make lint           # golangci-lint run
make clean          # remove build artifacts
make container      # podman build
make all            # lint + test + build
```

## Test Suite

Tests are organized by package, matching the production code structure:

| Package | Test File | Tests | Lines | Model Used |
|---------|-----------|-------|-------|------------|
| `config` | `config_test.go` | 13 | 596 | Haiku |
| `provider` | `provider_test.go` | 10 | 365 | Haiku |
| `download` | `client_test.go` | 13 | 530 | Sonnet |
| `download` | `pool_test.go` | 10 | 464 | Sonnet |
| `store` | `sqlite_test.go` | 40 | 1,472 | Sonnet |
| `engine` | `sync_test.go` | 17 | 1,053 | Sonnet |
| `provider/ocp` | `ocp_test.go` | 17 | 965 | Sonnet |
| **Total** | **7 files** | **120** | **5,445** | |

### Test Tier Strategy

Tests were written using different AI models based on complexity:

- **Haiku** — simple/mechanical tests (config parsing, registry CRUD, type assertions). Fast and cheap for straightforward test patterns.
- **Sonnet** — tests requiring understanding of complex logic (HTTP mocking, concurrent download pools, database integration, sync orchestration). Worth the cost for getting mock servers, race conditions, and integration patterns right.

### Running Specific Tests

```bash
# All tests
go test ./...

# Specific package
go test -v ./internal/config/...
go test -v ./internal/store/...
go test -v ./internal/download/...
go test -v ./internal/engine/...
go test -v ./internal/provider/ocp/...

# Specific test
go test -v -run TestSyncProviderDryRun ./internal/engine/...

# With race detector
go test -race ./...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## First Build Checklist

After cloning and entering `nix develop`:

```bash
# 1. Resolve dependencies
go mod tidy

# 2. Verify everything compiles
go build ./...

# 3. Run tests
go test ./...

# 4. Build the binary
go build -o airgap ./cmd/airgap

# 5. Verify it runs
./airgap --help
./airgap status
```

## CI/CD

The Makefile provides everything needed for GitHub Actions:

```yaml
# .github/workflows/ci.yml
name: CI
on: [push, pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: cachix/install-nix-action@v27
      - run: nix develop --command bash -c "go mod tidy && make all"
```

Or without Nix:

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go mod tidy
      - run: go test ./...
      - run: go build ./cmd/airgap
```
