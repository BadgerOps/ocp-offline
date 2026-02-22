# airgap - Unified Offline Sync Tool

A Go-based framework for managing offline content synchronization across multiple sources including EPEL repositories, OpenShift Container Platform binaries, container images, and custom files.

## Foundation Status

This is the **project foundation** with core interfaces, configuration system, and database models. All major packages are structured and ready for implementation.

## Quick Start

### Project Structure
```
/sessions/beautiful-clever-dijkstra/mnt/ocp-offline/
├── cmd/airgap/                    # Application entry point
│   └── main.go
├── internal/
│   ├── api/                       # REST API handlers (ready)
│   ├── config/
│   │   └── config.go             # Configuration system (implemented)
│   ├── engine/                    # Sync engine & scheduler (ready)
│   ├── provider/
│   │   ├── provider.go           # Core interface (implemented)
│   │   ├── epel/                 # EPEL provider (ready)
│   │   ├── ocp/                  # OCP provider (ready)
│   │   ├── containers/           # Container provider (ready)
│   │   └── custom/               # Custom files provider (ready)
│   ├── store/
│   │   └── models.go             # Database models (implemented)
│   ├── download/                 # Download engine (ready)
│   └── ui/                       # Web UI (ready)
│       ├── templates/            # HTML templates
│       └── static/               # CSS/JS/Images
├── configs/                       # Configuration examples
├── go.mod                         # Module definition
├── go.sum                         # Dependency checksums
├── PROJECT_SETUP.md              # Setup documentation
├── ARCHITECTURE.md               # System design
├── FILE_MANIFEST.txt             # File inventory
└── README.md                      # This file
```

## Implemented Components

### 1. Provider Interface (`internal/provider/provider.go`)
Core abstraction for all content sources.

**Interface Methods:**
- `Name()` - Provider identifier
- `Configure(cfg ProviderConfig)` - Load settings
- `Plan(ctx context.Context)` - Preview changes
- `Sync(ctx, plan, opts)` - Execute sync
- `Validate(ctx)` - Verify integrity

**Key Types:**
- `SyncPlan` - Preview of what will change (read-only)
- `SyncAction` - Single file operation (download, delete, skip, update)
- `SyncReport` - Execution results with metrics
- `ValidationReport` - Integrity check results
- `Registry` - Provider discovery and management

### 2. Configuration System (`internal/config/config.go`)
Unified YAML configuration for all providers.

**Top-Level Sections:**
```yaml
server:
  listen: "0.0.0.0:8080"
  data_dir: "/var/lib/airgap"
  db_path: "/var/lib/airgap/airgap.db"

export:
  split_size: "25GB"
  compression: "zstd"
  output_dir: "/mnt/transfer-disk"
  manifest_name: "airgap-manifest.json"

schedule:
  enabled: true
  default_cron: "0 2 * * 0"

providers:
  epel: { ... }
  ocp-binaries: { ... }
  rhcos: { ... }
  container-images: { ... }
  registry: { ... }
  custom-files: { ... }
```

**Features:**
- `DefaultConfig()` - Sensible defaults
- `Load(path)` - Load from YAML
- `FindConfigFile()` - Auto-discover config
- `ProviderEnabled(name)` - Check if enabled
- `ParseProviderConfig[T]()` - Type-safe unmarshaling

### 3. Database Models (`internal/store/models.go`)
Persistence layer for tracking state and history.

**Models:**
- `SyncRun` - Audit trail of sync operations
- `FileRecord` - Inventory of synced files with checksums
- `Job` - Scheduled and completed operations
- `Transfer` - Export/import archive operations
- `FailedFileRecord` - Dead letter queue for retries

## Design Patterns

1. **Provider Pattern** - Interface-based extensibility
2. **Registry Pattern** - Centralized provider discovery
3. **Plan-Execute Separation** - Preview before applying
4. **Type-Safe Configuration** - Generics for config unmarshaling
5. **Data Transfer Objects** - Structured sync data
6. **Dead Letter Queue** - Failed file tracking
7. **Audit Trail** - Comprehensive logging

## Key Features

- **Multi-Source Support**: EPEL, OCP, RHCOS, container images, custom sources
- **Unified Configuration**: Single YAML controls everything
- **Plan Preview**: See what will change before executing
- **Automatic Retry**: Configurable retries with dead letter queue
- **Checksum Verification**: SHA256 validation on all downloads
- **Concurrent Downloads**: Configurable worker pool per provider
- **Integrity Validation**: Offline verification of synced content
- **Comprehensive Audit Trail**: All operations tracked in database

## Installation & Building

### Prerequisites
- Go 1.21+
- YAML support (via gopkg.in/yaml.v3)

### Build
```bash
cd /sessions/beautiful-clever-dijkstra/mnt/ocp-offline

# Download dependencies
go mod download

# Build all packages
go build ./...

# Build binary
go build -o bin/airgap ./cmd/airgap

# Run tests
go test ./...
```

## Core Abstractions

### Provider Interface
All content sources implement this interface:
```go
type Provider interface {
    Name() string
    Configure(cfg ProviderConfig) error
    Plan(ctx context.Context) (*SyncPlan, error)
    Sync(ctx context.Context, plan *SyncPlan, opts SyncOptions) (*SyncReport, error)
    Validate(ctx context.Context) (*ValidationReport, error)
}
```

### Sync Workflow
1. **Plan Phase**: Provider.Plan() → SyncPlan (what will change)
2. **Execute Phase**: Provider.Sync() → SyncReport (what happened)
3. **Validate Phase**: Provider.Validate() → ValidationReport (integrity check)

### Registry Pattern
```go
registry := provider.NewRegistry()
registry.Register(epelProvider)
registry.Register(ocpProvider)
registry.Register(customProvider)

// Discover providers
provider, found := registry.Get("epel")
allNames := registry.Names()
```

## Extension Points

### Adding a New Provider

1. Create package in `internal/provider/{name}/`
2. Implement `Provider` interface
3. Add config struct to `internal/config/config.go`
4. Add YAML section to configuration
5. Register in engine

Example:
```go
// internal/provider/myProvider/provider.go
type MyProvider struct {
    name   string
    config *MyProviderConfig
}

func (p *MyProvider) Name() string { ... }
func (p *MyProvider) Configure(cfg ProviderConfig) error { ... }
func (p *MyProvider) Plan(ctx context.Context) (*SyncPlan, error) { ... }
func (p *MyProvider) Sync(ctx, plan, opts) (*SyncReport, error) { ... }
func (p *MyProvider) Validate(ctx context.Context) (*ValidationReport, error) { ... }
```

## File Inventory

**Core Implementation (427 lines):**
- `internal/provider/provider.go` - 140 lines (Provider interface)
- `internal/config/config.go` - 199 lines (Configuration system)
- `internal/store/models.go` - 72 lines (Database models)
- `cmd/airgap/main.go` - 7 lines (Entry point)
- `go.mod` - 5 lines (Module definition)
- `go.sum` - 2 lines (Dependency checksums)

**Documentation:**
- `PROJECT_SETUP.md` - Setup guide
- `ARCHITECTURE.md` - System design details
- `FILE_MANIFEST.txt` - File inventory
- `README.md` - This file

## TODO / Known Limitations

- **Multi-architecture support**: Currently OCP binaries and RHCOS URLs are hardcoded to `x86_64`. Need to support discovering and downloading content for multiple architectures (x86_64, aarch64, ppc64le, s390x). This affects the mirror discovery service, provider configurations, and the UI (architecture selector for OCP/RHCOS providers).

## Next Steps

1. Implement engine package (sync orchestration)
2. Implement download package (concurrent downloads, retry)
3. Implement store package (SQLite persistence)
4. Implement individual providers:
   - EPEL repository provider
   - OCP binaries provider
   - RHCOS images provider
   - Container images provider
   - Custom files provider
5. Implement API package (REST endpoints)
6. Implement UI package (web interface)
7. Add tests and integration tests

## Development Notes

- All packages follow standard Go conventions
- Interface-based design enables pluggable implementations
- Configuration uses gopkg.in/yaml.v3 for YAML support
- Database models ready for SQLite integration
- Structured error handling with context support
- Concurrent operation support via sync options

## License

See LICENSE file (if applicable)

## Support

For detailed architecture, see [ARCHITECTURE.md](ARCHITECTURE.md)
For setup instructions, see [PROJECT_SETUP.md](PROJECT_SETUP.md)
For file inventory, see [FILE_MANIFEST.txt](FILE_MANIFEST.txt)
