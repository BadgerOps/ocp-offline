package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

// New creates a new Store, opening the SQLite database and running migrations
func New(dbPath string, logger *slog.Logger) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &Store{
		db:     db,
		logger: logger,
	}

	// Run migrations
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Info("Store initialized successfully", "path", dbPath)
	return s, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}

// ============================================================================
// SyncRun Operations
// ============================================================================

// CreateSyncRun inserts a new SyncRun and sets its ID
func (s *Store) CreateSyncRun(run *SyncRun) error {
	const query = `
		INSERT INTO sync_runs (
			provider, start_time, end_time, files_downloaded, files_deleted,
			files_skipped, files_failed, bytes_transferred, status, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		run.Provider, run.StartTime, run.EndTime, run.FilesDownloaded,
		run.FilesDeleted, run.FilesSkipped, run.FilesFailed,
		run.BytesTransferred, run.Status, run.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to insert sync run: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	run.ID = id
	return nil
}

// UpdateSyncRun updates an existing SyncRun by ID
func (s *Store) UpdateSyncRun(run *SyncRun) error {
	const query = `
		UPDATE sync_runs SET
			provider = ?, start_time = ?, end_time = ?, files_downloaded = ?,
			files_deleted = ?, files_skipped = ?, files_failed = ?,
			bytes_transferred = ?, status = ?, error_message = ?
		WHERE id = ?
	`

	result, err := s.db.Exec(
		query,
		run.Provider, run.StartTime, run.EndTime, run.FilesDownloaded,
		run.FilesDeleted, run.FilesSkipped, run.FilesFailed,
		run.BytesTransferred, run.Status, run.ErrorMessage, run.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update sync run: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("sync run not found: %d", run.ID)
	}

	return nil
}

// GetSyncRun retrieves a SyncRun by ID
func (s *Store) GetSyncRun(id int64) (*SyncRun, error) {
	const query = `
		SELECT id, provider, start_time, end_time, files_downloaded, files_deleted,
		       files_skipped, files_failed, bytes_transferred, status, error_message
		FROM sync_runs WHERE id = ?
	`

	run := &SyncRun{}
	err := s.db.QueryRow(query, id).Scan(
		&run.ID, &run.Provider, &run.StartTime, &run.EndTime,
		&run.FilesDownloaded, &run.FilesDeleted, &run.FilesSkipped,
		&run.FilesFailed, &run.BytesTransferred, &run.Status, &run.ErrorMessage,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("sync run not found: %d", id)
		}
		return nil, fmt.Errorf("failed to query sync run: %w", err)
	}

	return run, nil
}

// ListSyncRuns retrieves SyncRuns, optionally filtered by provider
func (s *Store) ListSyncRuns(provider string, limit int) ([]SyncRun, error) {
	query := `
		SELECT id, provider, start_time, end_time, files_downloaded, files_deleted,
		       files_skipped, files_failed, bytes_transferred, status, error_message
		FROM sync_runs
	`
	var args []interface{}

	if provider != "" {
		query += " WHERE provider = ?"
		args = append(args, provider)
	}

	query += " ORDER BY start_time DESC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sync runs: %w", err)
	}
	defer rows.Close()

	var runs []SyncRun
	for rows.Next() {
		run := SyncRun{}
		err := rows.Scan(
			&run.ID, &run.Provider, &run.StartTime, &run.EndTime,
			&run.FilesDownloaded, &run.FilesDeleted, &run.FilesSkipped,
			&run.FilesFailed, &run.BytesTransferred, &run.Status, &run.ErrorMessage,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sync run: %w", err)
		}
		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sync runs: %w", err)
	}

	return runs, nil
}

// ============================================================================
// FileRecord Operations
// ============================================================================

// UpsertFileRecord inserts or replaces a FileRecord
func (s *Store) UpsertFileRecord(rec *FileRecord) error {
	const query = `
		INSERT OR REPLACE INTO file_records (
			id, provider, path, size, sha256, last_modified, last_verified, sync_run_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	// Pass nil for ID when 0 so SQLite uses AUTOINCREMENT
	var idVal interface{}
	if rec.ID != 0 {
		idVal = rec.ID
	}

	result, err := s.db.Exec(
		query,
		idVal, rec.Provider, rec.Path, rec.Size, rec.SHA256,
		rec.LastModified, rec.LastVerified, rec.SyncRunID,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert file record: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}
	rec.ID = id

	return nil
}

// GetFileRecord retrieves a FileRecord by provider and path
func (s *Store) GetFileRecord(provider, path string) (*FileRecord, error) {
	const query = `
		SELECT id, provider, path, size, sha256, last_modified, last_verified, sync_run_id
		FROM file_records WHERE provider = ? AND path = ?
	`

	rec := &FileRecord{}
	err := s.db.QueryRow(query, provider, path).Scan(
		&rec.ID, &rec.Provider, &rec.Path, &rec.Size, &rec.SHA256,
		&rec.LastModified, &rec.LastVerified, &rec.SyncRunID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("file record not found: %s/%s", provider, path)
		}
		return nil, fmt.Errorf("failed to query file record: %w", err)
	}

	return rec, nil
}

// ListFileRecords retrieves all FileRecords for a provider
func (s *Store) ListFileRecords(provider string) ([]FileRecord, error) {
	const query = `
		SELECT id, provider, path, size, sha256, last_modified, last_verified, sync_run_id
		FROM file_records WHERE provider = ? ORDER BY path
	`

	rows, err := s.db.Query(query, provider)
	if err != nil {
		return nil, fmt.Errorf("failed to query file records: %w", err)
	}
	defer rows.Close()

	var records []FileRecord
	for rows.Next() {
		rec := FileRecord{}
		err := rows.Scan(
			&rec.ID, &rec.Provider, &rec.Path, &rec.Size, &rec.SHA256,
			&rec.LastModified, &rec.LastVerified, &rec.SyncRunID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan file record: %w", err)
		}
		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating file records: %w", err)
	}

	return records, nil
}

// DeleteFileRecord deletes a FileRecord by provider and path
func (s *Store) DeleteFileRecord(provider, path string) error {
	const query = "DELETE FROM file_records WHERE provider = ? AND path = ?"

	result, err := s.db.Exec(query, provider, path)
	if err != nil {
		return fmt.Errorf("failed to delete file record: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("file record not found: %s/%s", provider, path)
	}

	return nil
}

// CountFileRecords returns the count of FileRecords for a provider
func (s *Store) CountFileRecords(provider string) (int, error) {
	const query = "SELECT COUNT(*) FROM file_records WHERE provider = ?"

	var count int
	err := s.db.QueryRow(query, provider).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count file records: %w", err)
	}

	return count, nil
}

// SumFileSize returns the total size of all files for a provider
func (s *Store) SumFileSize(provider string) (int64, error) {
	const query = "SELECT COALESCE(SUM(size), 0) FROM file_records WHERE provider = ?"

	var totalSize int64
	err := s.db.QueryRow(query, provider).Scan(&totalSize)
	if err != nil {
		return 0, fmt.Errorf("failed to sum file size: %w", err)
	}

	return totalSize, nil
}

// ============================================================================
// FailedFileRecord Operations (Dead Letter Queue)
// ============================================================================

// AddFailedFile adds a new FailedFileRecord
func (s *Store) AddFailedFile(rec *FailedFileRecord) error {
	// Update existing unresolved record for the same provider+file, or insert new.
	const upsertQuery = `
		UPDATE failed_files
		SET error = ?, retry_count = retry_count + 1, last_failure = ?,
		    url = COALESCE(NULLIF(?, ''), url),
		    dest_path = COALESCE(NULLIF(?, ''), dest_path),
		    expected_checksum = COALESCE(NULLIF(?, ''), expected_checksum),
		    expected_size = CASE WHEN ? > 0 THEN ? ELSE expected_size END
		WHERE provider = ? AND file_path = ? AND resolved = 0
	`

	result, err := s.db.Exec(
		upsertQuery,
		rec.Error, rec.LastFailure,
		rec.URL, rec.DestPath, rec.ExpectedChecksum,
		rec.ExpectedSize, rec.ExpectedSize,
		rec.Provider, rec.FilePath,
	)
	if err != nil {
		return fmt.Errorf("failed to update failed file record: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		return nil // existing record updated
	}

	// No existing unresolved record — insert new
	const insertQuery = `
		INSERT INTO failed_files (
			provider, file_path, url, dest_path, expected_checksum, expected_size, error,
			retry_count, first_failure, last_failure, resolved
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err = s.db.Exec(
		insertQuery,
		rec.Provider, rec.FilePath, rec.URL, rec.DestPath, rec.ExpectedChecksum,
		rec.ExpectedSize, rec.Error, rec.RetryCount, rec.FirstFailure, rec.LastFailure,
		rec.Resolved,
	)
	if err != nil {
		return fmt.Errorf("failed to add failed file record: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	rec.ID = id
	return nil
}

// ListFailedFiles retrieves all FailedFileRecords for a provider
func (s *Store) ListFailedFiles(provider string) ([]FailedFileRecord, error) {
	const query = `
		SELECT id, provider, file_path, url, dest_path, expected_checksum, expected_size, error,
		       retry_count, first_failure, last_failure, resolved
		FROM failed_files WHERE provider = ? AND resolved = 0 ORDER BY last_failure DESC
	`

	rows, err := s.db.Query(query, provider)
	if err != nil {
		return nil, fmt.Errorf("failed to query failed files: %w", err)
	}
	defer rows.Close()

	var records []FailedFileRecord
	for rows.Next() {
		rec := FailedFileRecord{}
		err := rows.Scan(
			&rec.ID, &rec.Provider, &rec.FilePath, &rec.URL, &rec.DestPath,
			&rec.ExpectedChecksum, &rec.ExpectedSize, &rec.Error, &rec.RetryCount,
			&rec.FirstFailure, &rec.LastFailure, &rec.Resolved,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan failed file record: %w", err)
		}
		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating failed file records: %w", err)
	}

	return records, nil
}

// ResolveFailedFile marks a FailedFileRecord as resolved
func (s *Store) ResolveFailedFile(id int64) error {
	const query = "UPDATE failed_files SET resolved = 1 WHERE id = ?"

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to resolve failed file: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("failed file record not found: %d", id)
	}

	return nil
}

// IncrementFailedRetry increments the retry count and updates last_failure
func (s *Store) IncrementFailedRetry(id int64) error {
	const query = `
		UPDATE failed_files
		SET retry_count = retry_count + 1, last_failure = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to increment failed retry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("failed file record not found: %d", id)
	}

	return nil
}

// ============================================================================
// Job Operations
// ============================================================================

// CreateJob inserts a new Job and sets its ID
func (s *Store) CreateJob(job *Job) error {
	const query = `
		INSERT INTO jobs (
			type, provider, cron_expr, status, last_run, next_run, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		job.Type, job.Provider, job.CronExpr, job.Status,
		job.LastRun, job.NextRun, job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert job: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	job.ID = id
	return nil
}

// UpdateJob updates an existing Job by ID
func (s *Store) UpdateJob(job *Job) error {
	const query = `
		UPDATE jobs SET
			type = ?, provider = ?, cron_expr = ?, status = ?,
			last_run = ?, next_run = ?, created_at = ?, updated_at = ?
		WHERE id = ?
	`

	result, err := s.db.Exec(
		query,
		job.Type, job.Provider, job.CronExpr, job.Status,
		job.LastRun, job.NextRun, job.CreatedAt, job.UpdatedAt, job.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("job not found: %d", job.ID)
	}

	return nil
}

// ListJobs retrieves Jobs, optionally filtered by status
func (s *Store) ListJobs(status string, limit int) ([]Job, error) {
	query := `
		SELECT id, type, provider, cron_expr, status, last_run, next_run, created_at, updated_at
		FROM jobs
	`
	var args []interface{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}

	query += " ORDER BY next_run ASC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		job := Job{}
		err := rows.Scan(
			&job.ID, &job.Type, &job.Provider, &job.CronExpr, &job.Status,
			&job.LastRun, &job.NextRun, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	return jobs, nil
}

// ============================================================================
// Transfer Operations
// ============================================================================

// CreateTransfer inserts a new Transfer and sets its ID
func (s *Store) CreateTransfer(t *Transfer) error {
	const query = `
		INSERT INTO transfers (
			direction, path, providers, archive_count, total_size,
			manifest_hash, status, error_message, start_time, end_time
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		t.Direction, t.Path, t.Providers, t.ArchiveCount, t.TotalSize,
		t.ManifestHash, t.Status, t.ErrorMessage, t.StartTime, t.EndTime,
	)
	if err != nil {
		return fmt.Errorf("failed to insert transfer: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	t.ID = id
	return nil
}

// UpdateTransfer updates an existing Transfer by ID
func (s *Store) UpdateTransfer(t *Transfer) error {
	const query = `
		UPDATE transfers SET
			direction = ?, path = ?, providers = ?, archive_count = ?,
			total_size = ?, manifest_hash = ?, status = ?,
			error_message = ?, start_time = ?, end_time = ?
		WHERE id = ?
	`

	result, err := s.db.Exec(
		query,
		t.Direction, t.Path, t.Providers, t.ArchiveCount, t.TotalSize,
		t.ManifestHash, t.Status, t.ErrorMessage, t.StartTime, t.EndTime, t.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update transfer: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("transfer not found: %d", t.ID)
	}

	return nil
}

// ============================================================================
// TransferArchive Operations
// ============================================================================

// CreateTransferArchive inserts a new TransferArchive and sets its ID
func (s *Store) CreateTransferArchive(a *TransferArchive) error {
	const query = `
		INSERT INTO transfer_archives (
			transfer_id, archive_name, sha256, size, validated, validated_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		a.TransferID, a.ArchiveName, a.SHA256, a.Size,
		a.Validated, a.ValidatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert transfer archive: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	a.ID = id
	return nil
}

// MarkArchiveValidated marks a TransferArchive as validated
func (s *Store) MarkArchiveValidated(id int64) error {
	const query = `
		UPDATE transfer_archives
		SET validated = 1, validated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to mark archive validated: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("transfer archive not found: %d", id)
	}

	return nil
}

// ListTransferArchives retrieves all archives for a transfer
func (s *Store) ListTransferArchives(transferID int64) ([]TransferArchive, error) {
	const query = `
		SELECT id, transfer_id, archive_name, sha256, size, validated, validated_at
		FROM transfer_archives WHERE transfer_id = ? ORDER BY archive_name
	`

	rows, err := s.db.Query(query, transferID)
	if err != nil {
		return nil, fmt.Errorf("failed to query transfer archives: %w", err)
	}
	defer rows.Close()

	var archives []TransferArchive
	for rows.Next() {
		a := TransferArchive{}
		err := rows.Scan(
			&a.ID, &a.TransferID, &a.ArchiveName, &a.SHA256,
			&a.Size, &a.Validated, &a.ValidatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan transfer archive: %w", err)
		}
		archives = append(archives, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transfer archives: %w", err)
	}

	return archives, nil
}

// IsArchiveValidated checks whether an archive with the given path, name, and sha256
// has been previously validated in a completed transfer.
func (s *Store) IsArchiveValidated(path, archiveName, sha256 string) (bool, error) {
	const query = `
		SELECT COUNT(*) FROM transfer_archives ta
		JOIN transfers t ON ta.transfer_id = t.id
		WHERE t.path = ? AND ta.archive_name = ? AND ta.sha256 = ? AND ta.validated = 1
	`

	var count int
	if err := s.db.QueryRow(query, path, archiveName, sha256).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check archive validation: %w", err)
	}

	return count > 0, nil
}

// ListTransfers retrieves Transfers, optionally limited
func (s *Store) ListTransfers(limit int) ([]Transfer, error) {
	query := `
		SELECT id, direction, path, providers, archive_count, total_size,
		       manifest_hash, status, error_message, start_time, end_time
		FROM transfers ORDER BY start_time DESC
	`

	if limit > 0 {
		query += " LIMIT ?"
	}

	var rows *sql.Rows
	var err error

	if limit > 0 {
		rows, err = s.db.Query(query, limit)
	} else {
		rows, err = s.db.Query(query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query transfers: %w", err)
	}
	defer rows.Close()

	var transfers []Transfer
	for rows.Next() {
		t := Transfer{}
		err := rows.Scan(
			&t.ID, &t.Direction, &t.Path, &t.Providers, &t.ArchiveCount,
			&t.TotalSize, &t.ManifestHash, &t.Status, &t.ErrorMessage,
			&t.StartTime, &t.EndTime,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan transfer: %w", err)
		}
		transfers = append(transfers, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transfers: %w", err)
	}

	return transfers, nil
}

// ============================================================================
// ProviderConfig Operations
// ============================================================================

// CreateProviderConfig inserts a new ProviderConfig and sets its ID.
func (s *Store) CreateProviderConfig(pc *ProviderConfig) error {
	const query = `
		INSERT INTO provider_configs (name, type, enabled, config_json)
		VALUES (?, ?, ?, ?)
	`
	result, err := s.db.Exec(query, pc.Name, pc.Type, pc.Enabled, pc.ConfigJSON)
	if err != nil {
		return fmt.Errorf("failed to insert provider config: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}
	pc.ID = id
	return nil
}

// GetProviderConfig retrieves a ProviderConfig by name.
func (s *Store) GetProviderConfig(name string) (*ProviderConfig, error) {
	const query = `
		SELECT id, name, type, enabled, config_json, created_at, updated_at
		FROM provider_configs WHERE name = ?
	`
	pc := &ProviderConfig{}
	err := s.db.QueryRow(query, name).Scan(
		&pc.ID, &pc.Name, &pc.Type, &pc.Enabled,
		&pc.ConfigJSON, &pc.CreatedAt, &pc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("provider config not found: %s: %w", name, err)
	}
	return pc, nil
}

// ListProviderConfigs retrieves all ProviderConfigs ordered by name.
func (s *Store) ListProviderConfigs() ([]ProviderConfig, error) {
	const query = `
		SELECT id, name, type, enabled, config_json, created_at, updated_at
		FROM provider_configs ORDER BY name
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query provider configs: %w", err)
	}
	defer rows.Close()

	var configs []ProviderConfig
	for rows.Next() {
		pc := ProviderConfig{}
		if err := rows.Scan(&pc.ID, &pc.Name, &pc.Type, &pc.Enabled,
			&pc.ConfigJSON, &pc.CreatedAt, &pc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan provider config: %w", err)
		}
		configs = append(configs, pc)
	}
	return configs, rows.Err()
}

// UpdateProviderConfig updates an existing ProviderConfig by ID.
func (s *Store) UpdateProviderConfig(pc *ProviderConfig) error {
	const query = `
		UPDATE provider_configs SET
			name = ?, type = ?, enabled = ?, config_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	result, err := s.db.Exec(query, pc.Name, pc.Type, pc.Enabled, pc.ConfigJSON, pc.ID)
	if err != nil {
		return fmt.Errorf("failed to update provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %d", pc.ID)
	}
	return nil
}

// DeleteProviderConfig deletes a ProviderConfig by name.
func (s *Store) DeleteProviderConfig(name string) error {
	const query = `DELETE FROM provider_configs WHERE name = ?`
	result, err := s.db.Exec(query, name)
	if err != nil {
		return fmt.Errorf("failed to delete provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %s", name)
	}
	return nil
}

// ToggleProviderConfig flips the enabled state of a provider.
func (s *Store) ToggleProviderConfig(name string) error {
	const query = `
		UPDATE provider_configs
		SET enabled = CASE WHEN enabled = 1 THEN 0 ELSE 1 END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE name = ?
	`
	result, err := s.db.Exec(query, name)
	if err != nil {
		return fmt.Errorf("failed to toggle provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %s", name)
	}
	return nil
}

// CountProviderConfigs returns the number of provider configs.
func (s *Store) CountProviderConfigs() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM provider_configs").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count provider configs: %w", err)
	}
	return count, nil
}

// SeedProviderConfigs populates provider_configs from a YAML providers map.
// This is a no-op if the table already has rows.
func (s *Store) SeedProviderConfigs(yamlProviders map[string]map[string]interface{}) error {
	count, err := s.CountProviderConfigs()
	if err != nil {
		return err
	}
	if count > 0 {
		s.logger.Info("provider_configs table already populated, skipping seed")
		return nil
	}

	knownTypes := map[string]bool{
		"epel": true, "ocp_binaries": true, "ocp_clients": true, "rhcos": true,
		"container_images": true, "registry": true, "custom_files": true,
	}

	for name, rawCfg := range yamlProviders {
		provType := name
		if !knownTypes[provType] {
			provType = "custom_files"
		}

		enabled := false
		if e, ok := rawCfg["enabled"].(bool); ok {
			enabled = e
		}

		configJSON, err := json.Marshal(rawCfg)
		if err != nil {
			s.logger.Warn("failed to marshal provider config for seeding", "name", name, "error", err)
			continue
		}

		pc := &ProviderConfig{
			Name:       name,
			Type:       provType,
			Enabled:    enabled,
			ConfigJSON: string(configJSON),
		}
		if err := s.CreateProviderConfig(pc); err != nil {
			s.logger.Warn("failed to seed provider config", "name", name, "error", err)
		}
	}

	s.logger.Info("seeded provider configs from YAML", "count", len(yamlProviders))
	return nil
}
