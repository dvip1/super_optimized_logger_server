package database

import "fmt"

// schema holds every CREATE statement. Add new tables/indexes to this list.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		email      TEXT PRIMARY KEY,
		password   TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE TABLE IF NOT EXISTS token_device (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		email   TEXT NOT NULL,
		token   BLOB NOT NULL UNIQUE,
		name    TEXT NOT NULL,
		revoked INTEGER NOT NULL DEFAULT 0,

		FOREIGN KEY (email)
		REFERENCES users(email)
		ON DELETE CASCADE
	)`,

	`CREATE TABLE IF NOT EXISTS devices (
		srno           BLOB PRIMARY KEY,
		device_name    TEXT,
		longitude      REAL,
		latitude       REAL,
		email          TEXT NOT NULL,
		token_id       INTEGER NOT NULL,
		encryption_key BLOB NOT NULL,

		FOREIGN KEY (email)
		REFERENCES users(email)
		ON DELETE CASCADE,

		FOREIGN KEY (token_id)
		REFERENCES token_device(id)
	)WITHOUT ROWID`,

	// Tier 1: append-only, arrival order. Every insert hits the rightmost
	// leaf of the rowid B-tree, so writes stay cheap.
	`CREATE TABLE IF NOT EXISTS ingest_log (
		id      INTEGER PRIMARY KEY,
		srno    BLOB NOT NULL,
		ts      INTEGER NOT NULL,
		sensors BLOB NOT NULL -- 256 raw bytes, plaintext
	)`,

	// Tier 2: one row per device per ~60 readings, compressed.
	`CREATE TABLE IF NOT EXISTS reading_blocks (
		srno     BLOB NOT NULL,
		start_ts INTEGER NOT NULL,
		end_ts   INTEGER NOT NULL,
		n        INTEGER NOT NULL,
		data     BLOB NOT NULL,
		PRIMARY KEY (srno, start_ts)
	) WITHOUT ROWID`,

	// Indexes for fast dashboard/ownership queries
	`CREATE INDEX IF NOT EXISTS idx_token_device_email ON token_device(email)`,
	`CREATE INDEX IF NOT EXISTS idx_devices_email ON devices(email)`,
	`CREATE INDEX IF NOT EXISTS idx_devices_token_id ON devices(token_id)`,
	// Per-device lookups for compaction deletes and crash-recovery replay
	`CREATE INDEX IF NOT EXISTS idx_ingest_log_srno_ts ON ingest_log(srno, ts)`,
}

// Seed creates all tables if they don't exist. Call it after Init.
// Runs in a transaction, so either every statement applies or none do.
func Seed() error {
	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("begin seed: %w", err)
	}
	defer tx.Rollback()

	for _, stmt := range schema {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("seed statement failed: %w\n%s", err, stmt)
		}
	}
	return tx.Commit()
}
