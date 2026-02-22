package engine

import (
	"sort"
	"sync"
	"time"
)

// SyncPhase represents the current phase of a sync operation.
type SyncPhase string

const (
	PhasePlanning    SyncPhase = "planning"
	PhaseDownloading SyncPhase = "downloading"
	PhaseComplete    SyncPhase = "complete"
	PhaseFailed      SyncPhase = "failed"
	PhaseCancelled   SyncPhase = "cancelled"
)

// FileEvent records a completed or failed file for the recent activity log.
type FileEvent struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "completed", "failed"
	Error  string `json:"error,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// SyncProgress is a snapshot of the current sync state, safe for JSON serialization.
type SyncProgress struct {
	Provider        string        `json:"provider"`
	Phase           SyncPhase     `json:"phase"`
	TotalFiles      int           `json:"total_files"`
	CompletedFiles  int           `json:"completed_files"`
	FailedFiles     int           `json:"failed_files"`
	SkippedFiles    int           `json:"skipped_files"`
	TotalBytes      int64         `json:"total_bytes"`
	BytesDownloaded int64         `json:"bytes_downloaded"`
	Percent         float64       `json:"percent"`
	CurrentFiles    []FileProgress `json:"current_files,omitempty"`
	RecentEvents    []FileEvent    `json:"recent_events,omitempty"`
	TotalRetries    int           `json:"total_retries"`
	BytesPerSecond  int64         `json:"bytes_per_second"`
	ETA             string        `json:"eta,omitempty"`
	StartTime       time.Time     `json:"start_time"`
	Elapsed         string        `json:"elapsed"`
	Message         string        `json:"message,omitempty"`
}

// FileProgress tracks the download state of an individual file.
type FileProgress struct {
	Path            string `json:"path"`
	BytesDownloaded int64  `json:"bytes_downloaded"`
	TotalBytes      int64  `json:"total_bytes"`
	Done            bool   `json:"done"`
	Failed          bool   `json:"failed"`
}

// SyncTracker accumulates progress from pool workers in a thread-safe manner.
// SSE handlers use Wait() to block until new updates are available.
type SyncTracker struct {
	mu sync.Mutex

	provider        string
	phase           SyncPhase
	totalFiles      int
	completedFiles  int
	failedFiles     int
	skippedFiles    int
	totalBytes      int64
	bytesDownloaded int64
	totalRetries    int
	startTime       time.Time
	message         string

	// Per-file progress keyed by dest path
	files map[string]*FileProgress

	// Rolling log of recent completed/failed files (capped at 20)
	recentEvents []FileEvent

	// Notification channel: close-and-replace pattern.
	// Listeners call Wait() to get the current channel, then block on it.
	// Any update closes the old channel and replaces it with a new one.
	notify chan struct{}

	// Throttle per-file byte updates to reduce lock contention.
	// Key: dest path, Value: last update time.
	lastFileUpdate map[string]time.Time
}

// NewSyncTracker creates a tracker for the given provider.
func NewSyncTracker(providerName string) *SyncTracker {
	return &SyncTracker{
		provider:       providerName,
		phase:          PhasePlanning,
		startTime:      time.Now(),
		files:          make(map[string]*FileProgress),
		notify:         make(chan struct{}),
		lastFileUpdate: make(map[string]time.Time),
	}
}

// Snapshot returns a copy of the current progress state.
func (t *SyncTracker) Snapshot() SyncProgress {
	t.mu.Lock()
	defer t.mu.Unlock()

	var pct float64
	workItems := t.totalFiles - t.skippedFiles // files that actually need processing
	if workItems > 0 {
		pct = float64(t.completedFiles+t.failedFiles) / float64(workItems) * 100
	} else if t.totalFiles > 0 {
		// All files skipped (nothing to do) — 100% complete
		pct = 100
	}

	currentFiles := make([]FileProgress, 0, len(t.files))
	for _, fp := range t.files {
		if !fp.Done && !fp.Failed {
			currentFiles = append(currentFiles, *fp)
		}
	}
	sort.Slice(currentFiles, func(i, j int) bool {
		return currentFiles[i].Path < currentFiles[j].Path
	})

	recentEvents := make([]FileEvent, len(t.recentEvents))
	copy(recentEvents, t.recentEvents)

	// Calculate download speed and ETA
	elapsed := time.Since(t.startTime)
	var bytesPerSecond int64
	var eta string
	if elapsed > time.Second && t.bytesDownloaded > 0 {
		bytesPerSecond = int64(float64(t.bytesDownloaded) / elapsed.Seconds())
		if bytesPerSecond > 0 && t.totalBytes > t.bytesDownloaded {
			remaining := t.totalBytes - t.bytesDownloaded
			etaDuration := time.Duration(float64(remaining) / float64(bytesPerSecond) * float64(time.Second))
			eta = etaDuration.Truncate(time.Second).String()
		}
	}

	return SyncProgress{
		Provider:        t.provider,
		Phase:           t.phase,
		TotalFiles:      t.totalFiles,
		CompletedFiles:  t.completedFiles,
		FailedFiles:     t.failedFiles,
		SkippedFiles:    t.skippedFiles,
		TotalBytes:      t.totalBytes,
		BytesDownloaded: t.bytesDownloaded,
		Percent:         pct,
		CurrentFiles:    currentFiles,
		RecentEvents:    recentEvents,
		TotalRetries:    t.totalRetries,
		BytesPerSecond:  bytesPerSecond,
		ETA:             eta,
		StartTime:       t.startTime,
		Elapsed:         elapsed.Truncate(time.Second).String(),
		Message:         t.message,
	}
}

// Wait returns a channel that will be closed when the next update occurs.
// Callers should select on this channel alongside a timeout for heartbeats.
func (t *SyncTracker) Wait() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.notify
}

// signal closes the current notify channel and replaces it with a new one.
// Must be called with t.mu held.
func (t *SyncTracker) signal() {
	close(t.notify)
	t.notify = make(chan struct{})
}

// SetPhase updates the current sync phase.
func (t *SyncTracker) SetPhase(phase SyncPhase) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = phase
	t.signal()
}

// SetTotals sets the total file count and byte count after planning.
func (t *SyncTracker) SetTotals(totalFiles int, totalBytes int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalFiles = totalFiles
	t.totalBytes = totalBytes
	t.signal()
}

// SetMessage sets a human-readable status message.
func (t *SyncTracker) SetMessage(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.message = msg
	t.signal()
}

// UpdateFileProgress updates the byte-level progress for a single file.
// Throttled to ~250ms per file to reduce lock contention.
func (t *SyncTracker) UpdateFileProgress(destPath string, bytesDownloaded, totalBytes int64) {
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	// Throttle: skip update if less than 250ms since last update for this file
	if last, ok := t.lastFileUpdate[destPath]; ok && now.Sub(last) < 250*time.Millisecond {
		return
	}
	t.lastFileUpdate[destPath] = now

	fp, ok := t.files[destPath]
	if !ok {
		fp = &FileProgress{Path: destPath}
		t.files[destPath] = fp
	}
	fp.BytesDownloaded = bytesDownloaded
	fp.TotalBytes = totalBytes

	// Recalculate total bytes downloaded from all files
	var total int64
	for _, f := range t.files {
		total += f.BytesDownloaded
	}
	t.bytesDownloaded = total

	t.signal()
}

// addRecentEvent prepends an event to the rolling log, capping at 20. Must be called with t.mu held.
func (t *SyncTracker) addRecentEvent(ev FileEvent) {
	t.recentEvents = append([]FileEvent{ev}, t.recentEvents...)
	if len(t.recentEvents) > 20 {
		t.recentEvents = t.recentEvents[:20]
	}
}

// FileCompleted marks a file as successfully downloaded.
func (t *SyncTracker) FileCompleted(destPath string, bytesDownloaded int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	fp, ok := t.files[destPath]
	if !ok {
		fp = &FileProgress{Path: destPath}
		t.files[destPath] = fp
	}
	fp.Done = true
	fp.BytesDownloaded = bytesDownloaded

	t.completedFiles++

	// Recalculate total bytes downloaded
	var total int64
	for _, f := range t.files {
		total += f.BytesDownloaded
	}
	t.bytesDownloaded = total

	t.addRecentEvent(FileEvent{Path: destPath, Status: "completed", Size: bytesDownloaded})

	t.signal()
}

// FileFailed marks a file as failed with an error reason.
func (t *SyncTracker) FileFailed(destPath string, errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	fp, ok := t.files[destPath]
	if !ok {
		fp = &FileProgress{Path: destPath}
		t.files[destPath] = fp
	}
	fp.Failed = true

	t.failedFiles++
	t.addRecentEvent(FileEvent{Path: destPath, Status: "failed", Error: errMsg})
	t.signal()
}

// FileSkipped increments the skipped file counter.
func (t *SyncTracker) FileSkipped() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.skippedFiles++
	t.signal()
}

// SetSkippedFiles sets the skipped file count in bulk (one signal instead of N).
func (t *SyncTracker) SetSkippedFiles(count int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.skippedFiles = count
	t.signal()
}

// AddRetries increments the total retry counter.
func (t *SyncTracker) AddRetries(count int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalRetries += count
	t.signal()
}
