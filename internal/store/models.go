package store

import "time"

// SyncRun records a sync execution
type SyncRun struct {
	ID               int64
	Provider         string
	StartTime        time.Time
	EndTime          time.Time
	FilesDownloaded  int
	FilesDeleted     int
	FilesSkipped     int
	FilesFailed      int
	BytesTransferred int64
	Status           string // "success", "partial", "failed"
	ErrorMessage     string
}

// FileRecord tracks a downloaded file
type FileRecord struct {
	ID           int64
	Provider     string
	Path         string // relative to provider output dir
	Size         int64
	SHA256       string
	LastModified time.Time
	LastVerified time.Time
	SyncRunID    int64
}

// Job represents a scheduled or completed job
type Job struct {
	ID        int64
	Type      string // "sync", "validate", "export", "import"
	Provider  string // empty for "all providers" jobs
	CronExpr  string // for scheduled jobs
	Status    string // "scheduled", "running", "completed", "failed"
	LastRun   time.Time
	NextRun   time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Transfer records an export or import operation
type Transfer struct {
	ID           int64
	Direction    string // "export" or "import"
	Path         string // source/destination path
	Providers    string // comma-separated provider names
	ArchiveCount int
	TotalSize    int64
	ManifestHash string
	Status       string // "running", "completed", "failed"
	ErrorMessage string
	StartTime    time.Time
	EndTime      time.Time
}

// TransferArchive tracks per-archive validation state during import
type TransferArchive struct {
	ID          int64
	TransferID  int64
	ArchiveName string
	SHA256      string
	Size        int64
	Validated   bool
	ValidatedAt time.Time
}

// FailedFileRecord is a dead letter queue entry
type FailedFileRecord struct {
	ID               int64
	Provider         string
	FilePath         string
	URL              string
	ExpectedChecksum string
	Error            string
	RetryCount       int
	FirstFailure     time.Time
	LastFailure      time.Time
	Resolved         bool
}

// ProviderConfig stores a provider's configuration in the database.
type ProviderConfig struct {
	ID         int64
	Name       string
	Type       string // "epel", "ocp_binaries", "rhcos", "container_images", "registry", "custom_files"
	Enabled    bool
	ConfigJSON string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
