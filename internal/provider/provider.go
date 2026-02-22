package provider

import (
	"context"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
)

// ActionType represents what to do with a file during sync
type ActionType string

const (
	ActionDownload ActionType = "download"
	ActionDelete   ActionType = "delete"
	ActionSkip     ActionType = "skip"
	ActionUpdate   ActionType = "update"
)

// SyncAction represents a single file operation in a sync plan
type SyncAction struct {
	Path      string // relative path within provider output dir (used as DB key)
	LocalPath string // absolute filesystem path for downloads
	Action    ActionType
	Size      int64
	Checksum  string // expected SHA256
	Reason    string // human-readable reason (e.g. "new file", "checksum mismatch")
	URL       string // download URL (for download/update actions)
}

// SyncPlan is the output of Plan() — what will change without executing
type SyncPlan struct {
	Provider   string
	Actions    []SyncAction
	TotalSize  int64 // bytes to download
	TotalFiles int
	Timestamp  time.Time
}

// SyncOptions controls how Sync() executes
type SyncOptions struct {
	DryRun     bool
	Force      bool // re-download everything regardless of checksum
	MaxWorkers int
	RetryCount int
}

// FailedFile records a file that failed all retries
type FailedFile struct {
	Path     string
	URL      string
	Error    string
	Attempts int
}

// SyncReport is the result of Sync()
type SyncReport struct {
	Provider         string
	StartTime        time.Time
	EndTime          time.Time
	Downloaded       int
	Deleted          int
	Skipped          int
	Failed           []FailedFile
	BytesTransferred int64
}

// ValidationResult represents one file's validation outcome
type ValidationResult struct {
	Path      string
	LocalPath string // absolute filesystem path
	Expected  string
	Actual    string
	Valid     bool
	Size      int64
	URL       string // download URL for retry if invalid
}

// ValidationReport is the result of Validate()
type ValidationReport struct {
	Provider     string
	TotalFiles   int
	ValidFiles   int
	InvalidFiles []ValidationResult
	Timestamp    time.Time
}

// ValidationProgressFn is called during validation for each file checked.
// checked is the count so far, total is the expected total, path is the
// current file, valid indicates whether it passed validation.
type ValidationProgressFn func(checked, total int, path string, valid bool)

// ProviderConfig is an alias for config.ProviderConfig to avoid import cycles
// Both packages define the same underlying type: map[string]interface{}
type ProviderConfig = config.ProviderConfig

// NameSetter is an optional interface that providers can implement to allow
// their name to be overridden with the user-chosen config name.
type NameSetter interface {
	SetName(name string)
}

// ValidationProgressSetter is an optional interface that providers can implement
// to report per-file progress during validation.
type ValidationProgressSetter interface {
	SetValidationProgress(fn ValidationProgressFn)
}

// Provider is the core interface that all content types implement
type Provider interface {
	// Name returns the provider identifier (e.g., "epel", "ocp-binaries")
	Name() string

	// Type returns the provider content type (e.g., "rpm_repo", "binary")
	Type() string

	// Configure loads provider-specific settings from the unified config
	Configure(cfg ProviderConfig) error

	// Plan compares upstream manifest against local state, returns
	// a list of actions (download, delete, skip) without executing them
	Plan(ctx context.Context) (*SyncPlan, error)

	// Sync executes the plan — downloads, validates, retries
	Sync(ctx context.Context, plan *SyncPlan, opts SyncOptions) (*SyncReport, error)

	// Validate checks integrity of all local content against manifests
	Validate(ctx context.Context) (*ValidationReport, error)
}

// Registry holds all registered providers
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry using its Name().
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// RegisterAs adds a provider under an explicit name, overriding p.Name().
// Use this when the user-chosen config name differs from the provider's
// built-in type name (e.g. config name "epel-10" vs provider name "epel").
// If the provider implements NameSetter, its internal name is also updated
// so that logs and reports use the config name consistently.
func (r *Registry) RegisterAs(name string, p Provider) {
	if ns, ok := p.(NameSetter); ok {
		ns.SetName(name)
	}
	r.providers[name] = p
}

// Get returns a provider by name
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// All returns all registered providers
func (r *Registry) All() map[string]Provider {
	return r.providers
}

// Remove deletes a provider from the registry by name.
func (r *Registry) Remove(name string) {
	delete(r.providers, name)
}

// Names returns all registered provider names
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
