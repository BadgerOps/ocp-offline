package engine

import "time"

// TransferManifest describes a complete export for transfer to an air-gapped environment.
type TransferManifest struct {
	Version       string                      `json:"version"`
	Created       time.Time                   `json:"created"`
	SourceHost    string                      `json:"source_host"`
	Providers     map[string]ManifestProvider `json:"providers"`
	Archives      []ManifestArchive           `json:"archives"`
	TotalArchives int                         `json:"total_archives"`
	TotalSize     int64                       `json:"total_size"`
	FileInventory []ManifestFile              `json:"file_inventory"`
}

// ManifestProvider summarizes one provider's contribution to the export.
type ManifestProvider struct {
	Type      string `json:"type,omitempty"`
	FileCount int    `json:"file_count"`
	TotalSize int64  `json:"total_size"`
}

// ManifestArchive describes a single split archive in the export.
type ManifestArchive struct {
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
	Files  []string `json:"files"`
}

// ManifestFile is one entry in the full file inventory.
type ManifestFile struct {
	Provider string `json:"provider"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}
