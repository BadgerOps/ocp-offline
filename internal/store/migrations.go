package store

import (
	"fmt"
)

// migrate runs all pending migrations
func (s *Store) migrate() error {
	// Create migrations table if it doesn't exist
	createMigrationsTableSQL := `
		CREATE TABLE IF NOT EXISTS migrations (
			id INTEGER PRIMARY KEY,
			version INTEGER NOT NULL UNIQUE,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`

	if _, err := s.db.Exec(createMigrationsTableSQL); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get the current schema version
	var currentVersion int
	err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	s.logger.Info("Current schema version", "version", currentVersion)

	// Define all migrations
	migrations := []struct {
		version int
		sql     string
	}{
		{
			version: 1,
			sql: `
				CREATE TABLE sync_runs (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					provider TEXT NOT NULL,
					start_time DATETIME NOT NULL,
					end_time DATETIME,
					files_downloaded INTEGER DEFAULT 0,
					files_deleted INTEGER DEFAULT 0,
					files_skipped INTEGER DEFAULT 0,
					files_failed INTEGER DEFAULT 0,
					bytes_transferred INTEGER DEFAULT 0,
					status TEXT DEFAULT 'running',
					error_message TEXT
				);

				CREATE TABLE file_records (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					provider TEXT NOT NULL,
					path TEXT NOT NULL,
					size INTEGER DEFAULT 0,
					sha256 TEXT,
					last_modified DATETIME,
					last_verified DATETIME,
					sync_run_id INTEGER,
					UNIQUE(provider, path),
					FOREIGN KEY(sync_run_id) REFERENCES sync_runs(id)
				);

				CREATE TABLE jobs (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					type TEXT NOT NULL,
					provider TEXT,
					cron_expr TEXT,
					status TEXT DEFAULT 'scheduled',
					last_run DATETIME,
					next_run DATETIME,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);

				CREATE TABLE transfers (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					direction TEXT NOT NULL,
					path TEXT NOT NULL,
					providers TEXT,
					archive_count INTEGER DEFAULT 0,
					total_size INTEGER DEFAULT 0,
					manifest_hash TEXT,
					status TEXT DEFAULT 'running',
					error_message TEXT,
					start_time DATETIME NOT NULL,
					end_time DATETIME
				);

				CREATE TABLE failed_files (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					provider TEXT NOT NULL,
					file_path TEXT NOT NULL,
					url TEXT,
					expected_checksum TEXT,
					error TEXT,
					retry_count INTEGER DEFAULT 0,
					first_failure DATETIME NOT NULL,
					last_failure DATETIME NOT NULL,
					resolved BOOLEAN DEFAULT 0
				);
			`,
		},
		{
			version: 2,
			sql: `
				CREATE TABLE transfer_archives (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					transfer_id INTEGER NOT NULL,
					archive_name TEXT NOT NULL,
					sha256 TEXT NOT NULL,
					size INTEGER DEFAULT 0,
					validated BOOLEAN DEFAULT 0,
					validated_at DATETIME,
					FOREIGN KEY(transfer_id) REFERENCES transfers(id)
				);
			`,
		},
	}

	// Run pending migrations
	for _, mig := range migrations {
		if mig.version > currentVersion {
			s.logger.Info("Running migration", "version", mig.version)

			if err := s.runMigration(mig.version, mig.sql); err != nil {
				return fmt.Errorf("failed to run migration %d: %w", mig.version, err)
			}

			s.logger.Info("Migration completed", "version", mig.version)
		}
	}

	return nil
}

// runMigration executes a migration and records it
func (s *Store) runMigration(version int, sql string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute the migration SQL
	if _, err := tx.Exec(sql); err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	// Record the migration
	insertSQL := "INSERT INTO migrations (version) VALUES (?)"
	if _, err := tx.Exec(insertSQL, version); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration transaction: %w", err)
	}

	return nil
}
