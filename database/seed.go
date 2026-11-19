package database

import "fmt"

// schema holds every CREATE statement. Add new tables/indexes to this list.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		email         TEXT PRIMARY KEY,
		password       TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS devices(
		srno  TEXT PRIMARY KEY,
		device_name TEXT, 
		longitude REAL, 
		latitude REAL,
		email TEXT NOT NULL,
		encryption_key BLOB NOT NULL, 

		FOREIGN KEY (email)
		REFERENCES users(email)
		ON DELETE CASCADE
	)	`,
	`
	  CREATE TABLE IF NOT EXISTS device_logs(
	 	srno TEXT, 
		payload BLOB NOT NULL,
		timestamp INTEGER,
		
		PRIMARY KEY (srno, timestamp),

		FOREIGN KEY (srno)
		REFERENCES devices(srno)
		ON DELETE CASCADE) 
		
	  `,
	  `
	  CREATE TABLE IF NOT EXISTS token_device(
	 	id INTEGER PRIMARY KEY AUTOINCREMENT,
	 	email TEXT NOT NULL,
		token BLOB, 
		name TEXT, 
		
		UNIQUE(name, email),

		FOREIGN KEY (email)
		REFERENCES users(email)
		ON DELETE CASCADE
	  )  
	  `,
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
